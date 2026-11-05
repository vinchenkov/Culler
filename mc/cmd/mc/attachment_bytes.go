package main

// The conversation attachment byte read (`mc __attachment-bytes`) is a
// composed crossing, like the other host-file-plus-spine verbs: the claim is
// confirmed where the spine lives — directly when this process can open it,
// or through the warm helper as a private frame — while the published file is
// verified HERE, host-side, because the helper mounts the spine volume alone
// and can never see $MC_HOME/attachments.

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"mc/verbs"
)

// Seams so the broker path is testable without Docker, mirroring the ingress
// broker's own seams.
var (
	ensureAttachmentBytesHelper = ensureRuntimeHelper
	callAttachmentBytesHelper   = callPrivateHelper
)

func parseAttachmentBytesArgs(rest []string) (verbs.AttachmentBytesArgs, error) {
	if len(rest) != 4 {
		return verbs.AttachmentBytesArgs{},
			verbs.Usagef("usage: mc __attachment-bytes <session> <in|out> <sha256> <size>")
	}
	size, err := strconv.ParseInt(rest[3], 10, 64)
	if err != nil || strconv.FormatInt(size, 10) != rest[3] {
		return verbs.AttachmentBytesArgs{},
			verbs.Usagef("attachment size must be an exact decimal byte count")
	}
	a := verbs.AttachmentBytesArgs{
		Session: rest[0], Direction: rest[1], SHA256: rest[2], Size: size,
	}
	if err := a.Validate(); err != nil {
		return verbs.AttachmentBytesArgs{}, err
	}
	return a, nil
}

func runAttachmentBytesRead(rest []string, stdout, stderr io.Writer) int {
	a, err := parseAttachmentBytesArgs(rest)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	// Host scope only, evaluated before any crossing: the answer names a HOST
	// path, and a container asking would be asking about a namespace it is
	// already inside.
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(id, "mc __attachment-bytes"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("%v", err))
	}
	claim, err := resolveAttachmentBytesClaim(a)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	result, err := verbs.AttachmentBytesServe(home, a, claim)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}

func resolveAttachmentBytesClaim(a verbs.AttachmentBytesArgs) (verbs.AttachmentClaimRecord, error) {
	if !shouldDelegateToHelper() {
		record, err := withSpine(func(db *sql.DB) (any, error) {
			return verbs.AttachmentBytesClaim(db, a)
		})
		if err != nil {
			return verbs.AttachmentClaimRecord{}, err
		}
		return record.(verbs.AttachmentClaimRecord), nil
	}
	if _, err := ensureAttachmentBytesHelper(); err != nil {
		return verbs.AttachmentClaimRecord{}, verbs.Usagef("ensure warm helper: %v", err)
	}
	var claim verbs.AttachmentClaimRecord
	if err := callAttachmentBytesHelper("__attachment-bytes-spine", a, &claim); err != nil {
		return verbs.AttachmentClaimRecord{}, privateHelperVerbError("private attachment claim read", err)
	}
	return claim, nil
}

// runPrivateAttachmentBytesSpine is the helper-side half: one framed claim
// confirmation against the spine, no file authority, no path in or out.
func runPrivateAttachmentBytesSpine(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	if len(args) != 3 || args[1] != "--deadline-unix-ms" {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("private attachment claim read requires its broker deadline"))
	}
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr,
			verbs.Domainf("private attachment claim read requires the helper's fixed spine scope"))
	}
	deadlineMillis, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("private attachment claim deadline is invalid"))
	}
	deadlineAt := time.UnixMilli(deadlineMillis)
	remaining, ok := helperDeadlineBudget(deadlineAt, time.Now())
	if !ok {
		return privateHelperTimeoutExitCode
	}
	deadline := time.AfterFunc(remaining, func() { os.Exit(privateHelperTimeoutExitCode) })
	defer deadline.Stop()
	var request verbs.AttachmentBytesArgs
	if err := readPrivateFrame(stdin, &request); err != nil || request.Validate() != nil {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("invalid private attachment claim frame"))
	}
	result, err := withSpine(func(db *sql.DB) (any, error) {
		return verbs.AttachmentBytesClaim(db, request)
	})
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writePrivateFrame(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc: write private attachment claim frame:", err)
		return 2
	}
	return 0
}
