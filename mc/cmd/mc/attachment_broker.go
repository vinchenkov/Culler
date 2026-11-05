package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"mc/verbs"
)

type privateAttachmentIngressReserveRequest struct {
	OwnerGeneration string `json:"owner_generation"`
	Session         string `json:"session"`
}

type privateAttachmentAttemptAbortRequest struct {
	AttemptID       string `json:"attempt_id"`
	OwnerGeneration string `json:"owner_generation"`
	OwnerToken      string `json:"owner_token"`
	Session         string `json:"session"`
}

func (r privateAttachmentAttemptAbortRequest) valid() bool {
	return privateAttachmentSession(r.Session) &&
		privateAttachmentHex(r.AttemptID, 16) &&
		privateAttachmentHex(r.OwnerToken, 32) &&
		privateAttachmentGeneration(r.OwnerGeneration)
}

func (r privateAttachmentAttemptAbortRequest) args() verbs.AttachmentAttemptAbortArgs {
	return verbs.AttachmentAttemptAbortArgs{
		Session: r.Session, AttemptID: r.AttemptID,
		OwnerToken: r.OwnerToken, OwnerGeneration: r.OwnerGeneration,
	}
}

type privateAttachmentAttemptsExpireDueRequest struct {
	After           string `json:"after"`
	Limit           int    `json:"limit"`
	OwnerGeneration string `json:"owner_generation"`
}

func (r privateAttachmentAttemptsExpireDueRequest) valid() bool {
	return (r.After == "" || privateAttachmentHex(r.After, 16)) &&
		r.Limit >= 1 && r.Limit <= 128 &&
		privateAttachmentGeneration(r.OwnerGeneration)
}

type privateAttachmentTerminalsReconcileRequest struct {
	AfterBatchID    string `json:"after_batch_id"`
	Limit           int    `json:"limit"`
	OwnerGeneration string `json:"owner_generation"`
}

func (r privateAttachmentTerminalsReconcileRequest) after() (int64, bool) {
	value, err := strconv.ParseInt(r.AfterBatchID, 10, 64)
	return value, err == nil && value >= 0 &&
		strconv.FormatInt(value, 10) == r.AfterBatchID
}

func (r privateAttachmentTerminalsReconcileRequest) valid() bool {
	_, exact := r.after()
	return exact && r.Limit >= 1 && r.Limit <= 128 &&
		privateAttachmentGeneration(r.OwnerGeneration)
}

var (
	ensurePrivateAttachmentHelper      = ensureRuntimeHelper
	callPrivateAttachmentHelper        = callPrivateHelper
	openPrivateAttachmentSpine         = openPrivateAttachmentSpineProxy
	openPrivateAttachmentTerminalSpine = openPrivateAttachmentTerminalSpineProxy
	privateAttachmentRenewEvery        = 10 * time.Second
	privateAttachmentBodyTimeout       = 5 * time.Second
)

const privateAttachmentMaxObjectBytes int64 = 256 * 1024 * 1024

type privateAttachmentPublicationPublishRequest struct {
	AttemptID             string `json:"attempt_id"`
	AttemptOwnerToken     string `json:"attempt_owner_token"`
	OwnerGeneration       string `json:"owner_generation"`
	PublicationID         string `json:"publication_id"`
	PublicationOwnerToken string `json:"publication_owner_token"`
	Session               string `json:"session"`
	Size                  int64  `json:"size"`
}

func (r privateAttachmentPublicationPublishRequest) args() verbs.AttachmentPublicationPublishArgs {
	return verbs.AttachmentPublicationPublishArgs{
		Session: r.Session, AttemptID: r.AttemptID,
		AttemptOwnerToken: r.AttemptOwnerToken, PublicationID: r.PublicationID,
		PublicationOwnerToken: r.PublicationOwnerToken, OwnerGeneration: r.OwnerGeneration,
	}
}

func (r privateAttachmentPublicationPublishRequest) valid() bool {
	return privateAttachmentSession(r.Session) &&
		privateAttachmentHex(r.AttemptID, 16) &&
		privateAttachmentHex(r.AttemptOwnerToken, 32) &&
		privateAttachmentHex(r.PublicationID, 16) &&
		privateAttachmentHex(r.PublicationOwnerToken, 32) &&
		privateAttachmentGeneration(r.OwnerGeneration) &&
		r.Size >= 0 && r.Size <= privateAttachmentMaxObjectBytes
}

type privateAttachmentPublicationRenewRequest struct {
	AttemptID             string `json:"attempt_id"`
	AttemptOwnerToken     string `json:"attempt_owner_token"`
	OwnerGeneration       string `json:"owner_generation"`
	PublicationID         string `json:"publication_id"`
	PublicationOwnerToken string `json:"publication_owner_token"`
	Session               string `json:"session"`
}

func (r privateAttachmentPublicationRenewRequest) args() verbs.AttachmentPublicationRenewArgs {
	return verbs.AttachmentPublicationRenewArgs{
		Session: r.Session, AttemptID: r.AttemptID,
		AttemptOwnerToken: r.AttemptOwnerToken, PublicationID: r.PublicationID,
		PublicationOwnerToken: r.PublicationOwnerToken, OwnerGeneration: r.OwnerGeneration,
	}
}

func (r privateAttachmentPublicationRenewRequest) valid() bool {
	return privateAttachmentSession(r.Session) &&
		privateAttachmentHex(r.AttemptID, 16) &&
		privateAttachmentHex(r.AttemptOwnerToken, 32) &&
		privateAttachmentHex(r.PublicationID, 16) &&
		privateAttachmentHex(r.PublicationOwnerToken, 32) &&
		privateAttachmentGeneration(r.OwnerGeneration)
}

type privateAttachmentByteStream struct {
	source    io.Reader
	count     int64
	fence     *privateAttachmentRenewalFence
	closeOnce sync.Once
}

func (s *privateAttachmentByteStream) Read(body []byte) (int, error) {
	if s.fence != nil {
		if err := s.fence.Err(); err != nil {
			return 0, err
		}
	}
	type readResult struct {
		body []byte
		n    int
		err  error
	}
	completed := make(chan readResult, 1)
	go func() {
		chunk := make([]byte, len(body))
		n, err := s.source.Read(chunk)
		completed <- readResult{body: chunk, n: n, err: err}
	}()
	timer := time.NewTimer(privateAttachmentBodyTimeout)
	defer timer.Stop()
	var fenced <-chan struct{}
	if s.fence != nil {
		fenced = s.fence.Done()
	}
	select {
	case result := <-completed:
		if result.n == 0 && result.err == nil {
			return 0, io.ErrNoProgress
		}
		copy(body, result.body[:result.n])
		s.count += int64(result.n)
		return result.n, result.err
	case <-fenced:
		s.close()
		return 0, s.fence.Err()
	case <-timer.C:
		s.close()
		return 0, verbs.Usagef(
			"private attachment byte stream made no progress for %s",
			privateAttachmentBodyTimeout)
	}
}

func (s *privateAttachmentByteStream) close() {
	s.closeOnce.Do(func() {
		if closer, ok := s.source.(io.Closer); ok {
			_ = closer.Close()
		}
	})
}

func (s *privateAttachmentByteStream) drainExact(size int64) error {
	if s.count > size {
		return verbs.Usagef("private attachment byte stream exceeded its declared size")
	}
	if remaining := size - s.count; remaining > 0 {
		written, err := io.CopyN(io.Discard, s, remaining)
		if err != nil || written != remaining {
			return verbs.Usagef("private attachment byte stream ended before its declared size")
		}
	}
	var trailing [1]byte
	n, err := s.Read(trailing[:])
	if n != 0 || (err != nil && err != io.EOF) {
		return verbs.Usagef("private attachment byte stream exceeded its declared size")
	}
	return nil
}

type privateAttachmentRenewalFence struct {
	mu     sync.Mutex
	err    error
	failed chan struct{}
}

func newPrivateAttachmentRenewalFence() *privateAttachmentRenewalFence {
	return &privateAttachmentRenewalFence{failed: make(chan struct{})}
}

func (f *privateAttachmentRenewalFence) Fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == nil {
		f.err = err
		close(f.failed)
	}
}

func (f *privateAttachmentRenewalFence) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *privateAttachmentRenewalFence) Done() <-chan struct{} {
	return f.failed
}

func renewPrivateAttachmentPublication(
	request privateAttachmentPublicationPublishRequest,
) error {
	renewal := privateAttachmentPublicationRenewRequest{
		AttemptID:             request.AttemptID,
		AttemptOwnerToken:     request.AttemptOwnerToken,
		OwnerGeneration:       request.OwnerGeneration,
		PublicationID:         request.PublicationID,
		PublicationOwnerToken: request.PublicationOwnerToken,
		Session:               request.Session,
	}
	var receipt verbs.AttachmentPublicationLeaseReceipt
	return callPrivateAttachmentHelper(
		"__attachment-publication-renew-spine", renewal, &receipt)
}

func maintainPrivateAttachmentPublicationLease(
	request privateAttachmentPublicationPublishRequest,
	fence *privateAttachmentRenewalFence,
) (stop func() error) {
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(privateAttachmentRenewEvery)
		defer timer.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-timer.C:
				if err := renewPrivateAttachmentPublication(request); err != nil {
					fence.Fail(fmt.Errorf("renew attachment publication lease: %w", err))
					return
				}
				timer.Reset(privateAttachmentRenewEvery)
			}
		}
	}()
	var once sync.Once
	return func() error {
		once.Do(func() { close(stopped) })
		<-done
		return fence.Err()
	}
}

type privateAttachmentPublicationReservation struct {
	DisplayName   string `json:"display_name"`
	MediaType     string `json:"media_type"`
	Ordinal       int    `json:"ordinal"`
	OwnerToken    string `json:"owner_token"`
	PublicationID string `json:"publication_id"`
	SHA256        string `json:"sha256,omitempty"`
	Size          int64  `json:"size"`
}

type privateAttachmentIngressReservation struct {
	AttemptID    string                                    `json:"attempt_id"`
	BatchID      string                                    `json:"batch_id"`
	ByteCount    int64                                     `json:"byte_count"`
	HardDeadline string                                    `json:"hard_deadline"`
	IngressID    string                                    `json:"ingress_id"`
	LeaseUntil   string                                    `json:"lease_until"`
	ObjectCount  int                                       `json:"object_count"`
	OwnerToken   string                                    `json:"owner_token"`
	Publications []privateAttachmentPublicationReservation `json:"publications"`
	Replayed     bool                                      `json:"replayed"`
}

type privateAttachmentAttemptTerminalReceipt struct {
	AttemptID string `json:"attempt_id"`
	IngressID string `json:"ingress_id"`
	BatchID   string `json:"batch_id"`
	Attempt   int    `json:"attempt"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason"`
	Receipt   string `json:"receipt"`
	Replayed  bool   `json:"replayed"`
}

type privateAttachmentAttemptExpiryPage struct {
	Receipts []privateAttachmentAttemptTerminalReceipt `json:"receipts"`
	Next     string                                    `json:"next,omitempty"`
}

type privateAttachmentTerminalBatch struct {
	BatchID   string `json:"batch_id"`
	IngressID string `json:"ingress_id"`
	AttemptID string `json:"attempt_id"`
	Session   string `json:"session_id"`
	Outcome   string `json:"outcome"`
}

type privateAttachmentTerminalPage struct {
	Batches []privateAttachmentTerminalBatch `json:"batches"`
	Next    string                           `json:"next,omitempty"`
}

func privateAttachmentAttemptTerminalFrame(
	receipt verbs.AttachmentAttemptTerminalReceipt,
) privateAttachmentAttemptTerminalReceipt {
	return privateAttachmentAttemptTerminalReceipt{
		AttemptID: receipt.AttemptID,
		IngressID: strconv.FormatInt(receipt.IngressID, 10),
		BatchID:   strconv.FormatInt(receipt.BatchID, 10),
		Attempt:   receipt.Attempt,
		Outcome:   receipt.Outcome,
		Reason:    receipt.Reason,
		Receipt:   receipt.Receipt,
		Replayed:  receipt.Replayed,
	}
}

func privateAttachmentExpiryPageFrame(
	page verbs.AttachmentAttemptExpiryPage,
) privateAttachmentAttemptExpiryPage {
	out := privateAttachmentAttemptExpiryPage{
		Receipts: make([]privateAttachmentAttemptTerminalReceipt, 0, len(page.Receipts)),
		Next:     page.Next,
	}
	for _, receipt := range page.Receipts {
		out.Receipts = append(out.Receipts, privateAttachmentAttemptTerminalFrame(receipt))
	}
	return out
}

func privateAttachmentTerminalPageFrame(
	page verbs.AttachmentTerminalPage,
) privateAttachmentTerminalPage {
	out := privateAttachmentTerminalPage{
		Batches: make([]privateAttachmentTerminalBatch, 0, len(page.Batches)),
	}
	if page.Next != 0 {
		out.Next = strconv.FormatInt(page.Next, 10)
	}
	for _, batch := range page.Batches {
		out.Batches = append(out.Batches, privateAttachmentTerminalBatch{
			BatchID:   strconv.FormatInt(batch.BatchID, 10),
			IngressID: strconv.FormatInt(batch.IngressID, 10),
			AttemptID: batch.AttemptID,
			Session:   batch.Session,
			Outcome:   batch.Outcome,
		})
	}
	return out
}

func privateAttachmentReservationFrame(
	reservation verbs.AttachmentIngressReservation,
) privateAttachmentIngressReservation {
	publications := make([]privateAttachmentPublicationReservation, 0, len(reservation.Publications))
	for _, publication := range reservation.Publications {
		publications = append(publications, privateAttachmentPublicationReservation{
			DisplayName: publication.DisplayName, MediaType: publication.MediaType,
			Ordinal: publication.Ordinal, OwnerToken: publication.OwnerToken,
			PublicationID: publication.PublicationID, SHA256: publication.SHA256,
			Size: publication.Size,
		})
	}
	return privateAttachmentIngressReservation{
		AttemptID: reservation.AttemptID, BatchID: strconv.FormatInt(reservation.BatchID, 10),
		ByteCount: reservation.ByteCount, HardDeadline: reservation.HardDeadline,
		IngressID: strconv.FormatInt(reservation.IngressID, 10), LeaseUntil: reservation.LeaseUntil,
		ObjectCount: reservation.ObjectCount, OwnerToken: reservation.OwnerToken,
		Publications: publications, Replayed: reservation.Replayed,
	}
}

func privateAttachmentGeneration(value string) bool {
	return privateAttachmentHex(value, 32)
}

func privateAttachmentHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func privateAttachmentSession(value string) bool {
	if len(value) < 3 || len(value) > 255 || value[:2] != "h-" {
		return false
	}
	for _, char := range value[2:] {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

func requireCurrentAttachmentCanaryReceipt(generation string) (string, error) {
	home, err := configuredCanonicalHome()
	if err != nil {
		return "", verbs.Usagef("resolve attachment canary MC_HOME: %v", err)
	}
	root := filepath.Join(home, "health", "attachment-canary")
	if err := trustAttachmentCanaryDirs(home, filepath.Join(home, "health"), root); err != nil {
		return "", err
	}
	body, err := readAttachmentCanaryFile(filepath.Join(root, "last.json"), 0o600, 4096)
	if err != nil {
		return "", verbs.Domainf("current attachment canary receipt is unavailable: %v", err)
	}
	var receipt verbs.AttachmentCanaryReceipt
	if err := decodeAttachmentCanaryJSON(body, &receipt); err != nil ||
		receipt.Version != 1 ||
		!privateAttachmentGeneration(receipt.Generation) ||
		!privateAttachmentHex(receipt.InboundSHA256, 64) ||
		!privateAttachmentHex(receipt.OutboundSHA256, 64) ||
		receipt.RealUID != 10002 || receipt.EffectiveUID != 10001 ||
		receipt.SavedUID != 10001 || receipt.FSUID != 10001 ||
		!receipt.WarmInboundRead || !receipt.HostOutboundRead ||
		!receipt.PrivateGateHealthy || !receipt.RestartInboundRead ||
		!receipt.RestartOutboundRead || !receipt.OutsideSessions ||
		len(receipt.DeniedOutbound) != 5 ||
		receipt.DeniedOutbound[0] != "create:EACCES" ||
		receipt.DeniedOutbound[1] != "open:EACCES" ||
		receipt.DeniedOutbound[2] != "rename:EACCES" ||
		receipt.DeniedOutbound[3] != "traverse:EACCES" ||
		receipt.DeniedOutbound[4] != "unlink:EACCES" {
		return "", verbs.Domainf("current attachment canary receipt is malformed or red")
	}
	if receipt.Generation != generation {
		return "", verbs.Domainf("attachment owner generation is not current for this resident boot")
	}
	return home, nil
}

func acquireCurrentAttachmentCanaryGeneration(generation string) (*attachmentHealthLease, error) {
	home, err := requireCurrentAttachmentCanaryReceipt(generation)
	if err != nil {
		return nil, err
	}
	return acquireLiveAttachmentHealthGuard(home, generation)
}

func brokerPrivateAttachment(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("private attachment broker accepts only a framed request"))
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(id, "mc "+args[0]); err != nil {
		return writeVerbError(stdout, stderr, err)
	}

	switch args[0] {
	case "__attachment-attempt-abort":
		var request privateAttachmentAttemptAbortRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment abort frame"))
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		defer lease.Close()
		if _, err := ensurePrivateAttachmentHelper(); err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
		}
		var response privateAttachmentAttemptTerminalReceipt
		if err := callPrivateAttachmentHelper(
			"__attachment-attempt-abort-spine", request, &response,
		); err != nil {
			return writeVerbError(stdout, stderr,
				privateHelperVerbError("private attachment attempt abort", err))
		}
		if err := writeJSON(stdout, response); err != nil {
			fmt.Fprintln(stderr, "mc:", err)
			return 2
		}
		return 0
	case "__attachment-attempts-expire-due":
		var request privateAttachmentAttemptsExpireDueRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment expiry frame"))
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		defer lease.Close()
		if _, err := ensurePrivateAttachmentHelper(); err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
		}
		var response privateAttachmentAttemptExpiryPage
		if err := callPrivateAttachmentHelper(
			"__attachment-attempts-expire-due-spine", request, &response,
		); err != nil {
			return writeVerbError(stdout, stderr,
				privateHelperVerbError("private attachment hard-expiry sweep", err))
		}
		if err := writeJSON(stdout, response); err != nil {
			fmt.Fprintln(stderr, "mc:", err)
			return 2
		}
		return 0
	case "__attachment-terminals-reconcile":
		var request privateAttachmentTerminalsReconcileRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment reconciliation frame"))
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		defer lease.Close()
		if _, err := ensurePrivateAttachmentHelper(); err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
		}
		home, err := configuredCanonicalHome()
		if err != nil {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("resolve attachment MC_HOME: %v", err))
		}
		db, err := openPrivateAttachmentTerminalSpine()
		if err != nil {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("open private attachment terminal spine: %v", err))
		}
		after, _ := request.after()
		page, reconcileErr := verbs.AttachmentTerminalsReconcile(
			db, id, home, after, request.Limit)
		closeErr := db.Close()
		if reconcileErr != nil || closeErr != nil {
			return writeVerbError(stdout, stderr, errors.Join(reconcileErr, closeErr))
		}
		if err := writeJSON(stdout, privateAttachmentTerminalPageFrame(page)); err != nil {
			fmt.Fprintln(stderr, "mc:", err)
			return 2
		}
		return 0
	case "__attachment-ingress-reserve":
		var request privateAttachmentIngressReserveRequest
		if err := readPrivateFrame(stdin, &request); err != nil ||
			!privateAttachmentSession(request.Session) ||
			!privateAttachmentGeneration(request.OwnerGeneration) {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment reservation frame"))
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		defer lease.Close()
		if _, err := ensurePrivateAttachmentHelper(); err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
		}
		var response privateAttachmentIngressReservation
		if err := callPrivateAttachmentHelper("__attachment-ingress-reserve-spine", request, &response); err != nil {
			return writeVerbError(stdout, stderr,
				privateHelperVerbError("private attachment ingress reservation", err))
		}
		if err := writeJSON(stdout, response); err != nil {
			fmt.Fprintln(stderr, "mc:", err)
			return 2
		}
		return 0
	case "__attachment-publication-renew":
		var request privateAttachmentPublicationRenewRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment renewal frame"))
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		defer lease.Close()
		if _, err := ensurePrivateAttachmentHelper(); err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
		}
		var response verbs.AttachmentPublicationLeaseReceipt
		if err := callPrivateAttachmentHelper("__attachment-publication-renew-spine", request, &response); err != nil {
			return writeVerbError(stdout, stderr,
				privateHelperVerbError("private attachment publication renewal", err))
		}
		if err := writeJSON(stdout, response); err != nil {
			fmt.Fprintln(stderr, "mc:", err)
			return 2
		}
		return 0
	case "__attachment-publication-publish":
		var request privateAttachmentPublicationPublishRequest
		if err := readPrivateMessage(stdin, &request); err != nil || !request.valid() {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment publication frame"))
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		defer lease.Close()
		if _, err := ensurePrivateAttachmentHelper(); err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
		}
		if err := renewPrivateAttachmentPublication(request); err != nil {
			return writeVerbError(stdout, stderr,
				privateHelperVerbError("renew attachment publication lease", err))
		}
		home, err := configuredCanonicalHome()
		if err != nil {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("resolve attachment MC_HOME: %v", err))
		}
		db, err := openPrivateAttachmentSpine()
		if err != nil {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("open private attachment spine: %v", err))
		}
		fence := newPrivateAttachmentRenewalFence()
		stopRenewal := maintainPrivateAttachmentPublicationLease(request, fence)
		stream := &privateAttachmentByteStream{source: stdin, fence: fence}
		response, publishErr := verbs.AttachmentPublicationPublish(
			db, id, home, request.args(), stream)
		renewErr := stopRenewal()
		closeErr := db.Close()
		if publishErr != nil || closeErr != nil {
			return writeVerbError(stdout, stderr,
				errors.Join(publishErr, renewErr, closeErr))
		}
		if response.Size != request.Size {
			return writeVerbError(stdout, stderr,
				verbs.Domainf("attachment publication receipt size disagrees with its reservation"))
		}
		// Once materialization committed, a renewal racing that terminal state
		// may correctly refuse. It no longer fences transport-only draining of
		// a replay body the publisher did not need to consume.
		stream.fence = nil
		if err := stream.drainExact(request.Size); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := writeJSON(stdout, response); err != nil {
			fmt.Fprintln(stderr, "mc:", err)
			return 2
		}
		return 0
	}
	return writeVerbError(stdout, stderr, verbs.Usagef("unknown private attachment broker verb"))
}

func runPrivateAttachmentSpine(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	if len(args) != 3 || args[1] != "--deadline-unix-ms" {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("private attachment spine operation requires its broker deadline"))
	}
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr,
			verbs.Domainf("private attachment spine operation requires the helper's fixed spine scope"))
	}
	deadlineMillis, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("private attachment deadline is invalid"))
	}
	deadlineAt := time.UnixMilli(deadlineMillis)
	remaining, ok := helperDeadlineBudget(deadlineAt, time.Now())
	if !ok {
		return privateHelperTimeoutExitCode
	}
	deadline := time.AfterFunc(remaining, func() {
		// Match every other helper-owned short transaction: once the broker's
		// deadline wins, the process cannot later acquire the spine lock.
		os.Exit(privateHelperTimeoutExitCode)
	})
	defer deadline.Stop()

	switch args[0] {
	case "__attachment-attempt-abort-spine":
		var request privateAttachmentAttemptAbortRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment abort frame"))
		}
		result, err := withSpine(func(db *sql.DB) (any, error) {
			receipt, err := verbs.AttachmentAttemptAbort(db, nil, request.args())
			if err != nil {
				return nil, err
			}
			return privateAttachmentAttemptTerminalFrame(receipt), nil
		})
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := writePrivateFrame(stdout, result); err != nil {
			fmt.Fprintln(stderr, "mc: write private attachment abort frame:", err)
			return 2
		}
		return 0
	case "__attachment-attempts-expire-due-spine":
		var request privateAttachmentAttemptsExpireDueRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment expiry frame"))
		}
		result, err := withSpine(func(db *sql.DB) (any, error) {
			page, err := verbs.AttachmentAttemptsExpireDue(
				db, nil, request.After, request.Limit)
			if err != nil {
				return nil, err
			}
			return privateAttachmentExpiryPageFrame(page), nil
		})
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := writePrivateFrame(stdout, result); err != nil {
			fmt.Fprintln(stderr, "mc: write private attachment expiry frame:", err)
			return 2
		}
		return 0
	case "__attachment-ingress-reserve-spine":
		var request privateAttachmentIngressReserveRequest
		if err := readPrivateFrame(stdin, &request); err != nil ||
			!privateAttachmentGeneration(request.OwnerGeneration) ||
			!privateAttachmentSession(request.Session) {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment reservation frame"))
		}
		result, err := withSpine(func(db *sql.DB) (any, error) {
			reservation, err := verbs.AttachmentIngressReserve(db, nil, verbs.AttachmentIngressReserveArgs{
				Session: request.Session, OwnerGeneration: request.OwnerGeneration,
			})
			if err != nil {
				return nil, err
			}
			return privateAttachmentReservationFrame(reservation), nil
		})
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := writePrivateFrame(stdout, result); err != nil {
			fmt.Fprintln(stderr, "mc: write private attachment reservation frame:", err)
			return 2
		}
		return 0
	case "__attachment-publication-renew-spine":
		var request privateAttachmentPublicationRenewRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("invalid private attachment renewal frame"))
		}
		result, err := withSpine(func(db *sql.DB) (any, error) {
			return verbs.AttachmentPublicationRenew(db, nil, request.args())
		})
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := writePrivateFrame(stdout, result); err != nil {
			fmt.Fprintln(stderr, "mc: write private attachment renewal frame:", err)
			return 2
		}
		return 0
	}
	return writeVerbError(stdout, stderr, verbs.Usagef("unknown private attachment spine verb"))
}
