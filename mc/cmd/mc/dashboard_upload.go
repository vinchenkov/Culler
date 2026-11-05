package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"mc/verbs"
)

const dashboardUploadLimit = 16 << 20

type dashboardUploadRequest struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Body      string `json:"body"`
	Files     []struct {
		Name      string `json:"name"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"files"`
}

func commandDashboardUpload(args []string, stdin io.Reader) (any, error) {
	identity, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(identity, "Dashboard upload"); err != nil {
		return nil, err
	}
	if len(args) != 0 || !canInspectProjectVision() {
		return nil, verbs.Usagef("The upload requires the Culler host.")
	}
	var req dashboardUploadRequest
	decoder := json.NewDecoder(io.LimitReader(stdin, 23<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return nil, verbs.Usagef("The upload request is invalid or too large.")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, verbs.Usagef("The upload request contains extra data.")
	}
	if !privateAttachmentSession(req.SessionID) || len(req.RequestID) == 0 || len(req.RequestID) > 128 ||
		!utf8.ValidString(req.RequestID) || len(req.Body) > 65536 || !utf8.ValidString(req.Body) || len(req.Files) == 0 || len(req.Files) > 32 {
		return nil, verbs.Usagef("Select files and retry the upload in its conversation.")
	}
	bodies := make([][]byte, len(req.Files))
	manifest := make([]verbs.AttachmentManifestEntry, len(req.Files))
	total := 0
	for i, file := range req.Files {
		body, err := base64.StdEncoding.Strict().DecodeString(file.Data)
		if err != nil {
			return nil, verbs.Usagef("An upload contains invalid file data.")
		}
		total += len(body)
		if total > dashboardUploadLimit {
			return nil, verbs.Usagef("Select files with a combined size of at most 16 MiB.")
		}
		bodies[i] = body
		mediaType := file.MediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		manifest[i] = verbs.AttachmentManifestEntry{Ordinal: i + 1, Size: int64(len(body)), MediaType: mediaType, DisplayName: file.Name}
	}
	manifestBody, _ := json.Marshal(manifest)
	if _, err := verbs.ParseAttachmentManifest(string(manifestBody)); err != nil {
		return nil, err
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, err
	}
	sessionDigest := sha256.Sum256([]byte(req.SessionID))
	lock, err := os.OpenFile(filepath.Join(home, "dashboard-upload-"+hex.EncodeToString(sessionDigest[:])+".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, verbs.Domainf("Another upload is active in this conversation. Retry when it completes.")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	health, err := readAttachmentCanaryFile(filepath.Join(home, "health", "attachment-canary", "last.json"), 0600, 4096)
	if err != nil {
		return nil, verbs.Domainf("File access is unavailable. Retry when the resident completes its file check.")
	}
	var receipt verbs.AttachmentCanaryReceipt
	if err := decodeAttachmentCanaryJSON(health, &receipt); err != nil {
		return nil, err
	}
	lease, err := acquireCurrentAttachmentCanaryGeneration(receipt.Generation)
	if err != nil {
		return nil, err
	}
	lease.Close()
	catalog, err := dashboardUploadCommand(nil, "homie", "list")
	if err != nil {
		return nil, err
	}
	binding, channel := "", ""
	for _, raw := range catalog["sessions"].([]any) {
		session := raw.(map[string]any)
		if session["id"] != req.SessionID {
			continue
		}
		if session["status"] != "active" {
			for _, raw := range session["known_bindings"].([]any) {
				bound := raw.(map[string]any)
				if bound["surface"] != "dashboard" {
					continue
				}
				ref, _ := bound["channel_ref"].(string)
				if ref == "" {
					continue
				}
				if _, err := dashboardUploadCommand(nil, "homie", "resume", req.SessionID, "--from", "dashboard:"+ref); err != nil {
					return nil, err
				}
				current, err := dashboardUploadCommand(nil, "homie", "list")
				if err != nil {
					return nil, err
				}
				for _, raw := range current["sessions"].([]any) {
					row := raw.(map[string]any)
					if row["id"] == req.SessionID {
						session = row
						break
					}
				}
				break
			}
		}
		for _, raw := range session["active_bindings"].([]any) {
			bound := raw.(map[string]any)
			if bound["surface"] == "dashboard" {
				binding, _ = bound["binding_id"].(string)
				channel, _ = bound["channel_ref"].(string)
				break
			}
		}
	}
	if binding == "" || channel == "" {
		return nil, verbs.Domainf("Open this conversation before you upload files.")
	}
	sealed, _ := json.Marshal(req)
	digest := sha256.Sum256(sealed)
	messageID := "dashboard-" + hex.EncodeToString(digest[:])
	enqueued, err := dashboardUploadCommand(nil, "attachment", "ingress-enqueue", req.SessionID,
		"--binding", binding, "--surface", "dashboard", "--message-id", messageID, "--body", req.Body, "--manifest", string(manifestBody))
	if err != nil {
		return nil, err
	}
	if enqueued["state"] == "consumed" {
		return enqueued["result"], nil
	}
	if enqueued["state"] == "failed" {
		return nil, verbs.Domainf("This upload expired. Select the files again to start another upload.")
	}
	var frame bytes.Buffer
	if err := writePrivateFrame(&frame, privateAttachmentIngressReserveRequest{Session: req.SessionID, OwnerGeneration: receipt.Generation}); err != nil {
		return nil, err
	}
	reserved, err := dashboardUploadCommand(&frame, "__attachment-ingress-reserve")
	if err != nil {
		return nil, err
	}
	var reservation privateAttachmentIngressReservation
	encoded, _ := json.Marshal(reserved)
	if err := json.Unmarshal(encoded, &reservation); err != nil {
		return nil, err
	}
	if reservation.IngressID != fmt.Sprint(enqueued["ingress_id"]) || len(reservation.Publications) != len(bodies) {
		return nil, verbs.Domainf("Another upload awaits completion. Retry that upload before this one.")
	}
	for i, publication := range reservation.Publications {
		if publication.Ordinal != i+1 || publication.Size != int64(len(bodies[i])) {
			return nil, verbs.Domainf("The upload reservation differs from the selected files.")
		}
		frame.Reset()
		request := privateAttachmentPublicationPublishRequest{
			Session: req.SessionID, OwnerGeneration: receipt.Generation, AttemptID: reservation.AttemptID,
			AttemptOwnerToken: reservation.OwnerToken, PublicationID: publication.PublicationID,
			PublicationOwnerToken: publication.OwnerToken, Size: publication.Size,
		}
		if err := writePrivateFrame(&frame, request); err != nil {
			return nil, err
		}
		if _, err := dashboardUploadCommand(io.MultiReader(&frame, bytes.NewReader(bodies[i])), "__attachment-publication-publish"); err != nil {
			return nil, err
		}
	}
	return dashboardUploadCommand(nil, "homie", "send", req.SessionID, "--from", "dashboard:"+channel, "--body", req.Body, "--ingress-attempt", reservation.AttemptID)
}

func dashboardUploadCommand(stdin io.Reader, args ...string) (map[string]any, error) {
	var out, diagnostic bytes.Buffer
	if run(args, stdin, &out, &diagnostic) != 0 {
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(out.Bytes(), &envelope)
		if envelope.Error.Message != "" {
			return nil, verbs.Domainf("%s", envelope.Error.Message)
		}
		return nil, verbs.Domainf("The upload failed: %s", strings.TrimSpace(diagnostic.String()))
	}
	var result map[string]any
	decoder := json.NewDecoder(&out)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}
