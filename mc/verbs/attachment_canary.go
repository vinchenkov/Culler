package verbs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const AttachmentCanaryFrameVersion = 1

type AttachmentCanaryFrame struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
}

type AttachmentCanaryPublisherReceipt struct {
	Version      int    `json:"version"`
	Generation   string `json:"generation"`
	SHA256       string `json:"sha256"`
	RelativePath string `json:"relative_path"`
	RealUID      int    `json:"real_uid"`
	EffectiveUID int    `json:"effective_uid"`
	SavedUID     int    `json:"saved_uid"`
	FSUID        int    `json:"fs_uid"`
}

type AttachmentCanaryReceipt struct {
	Version             int      `json:"version"`
	Generation          string   `json:"generation"`
	InboundSHA256       string   `json:"inbound_sha256"`
	OutboundSHA256      string   `json:"outbound_sha256"`
	RealUID             int      `json:"real_uid"`
	EffectiveUID        int      `json:"effective_uid"`
	SavedUID            int      `json:"saved_uid"`
	FSUID               int      `json:"fs_uid"`
	WarmInboundRead     bool     `json:"warm_inbound_read"`
	HostOutboundRead    bool     `json:"host_outbound_read"`
	PrivateGateHealthy  bool     `json:"private_gate_healthy"`
	DeniedOutbound      []string `json:"denied_outbound"`
	RestartInboundRead  bool     `json:"restart_inbound_read"`
	RestartOutboundRead bool     `json:"restart_outbound_read"`
	OutsideSessions     bool     `json:"outside_sessions"`
}

func ValidateAttachmentCanaryFrame(frame AttachmentCanaryFrame) error {
	if frame.Version != AttachmentCanaryFrameVersion ||
		!safeAttachmentHex(frame.Generation, 32) {
		return Domainf("attachment canary frame is malformed or unsupported")
	}
	return nil
}

func AttachmentCanaryInboundBytes() []byte {
	return []byte("culler attachment canary inbound v1\n")
}

func AttachmentCanaryOutboundBytes(frame AttachmentCanaryFrame) ([]byte, error) {
	if err := ValidateAttachmentCanaryFrame(frame); err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(
		"culler attachment canary outbound v1 generation=%s\n",
		frame.Generation,
	)), nil
}

func AttachmentCanaryDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func AttachmentCanaryRelativePath(digest string) (string, error) {
	if !safeAttachmentHex(digest, sha256.Size*2) {
		return "", Domainf("attachment canary digest is malformed")
	}
	return "published/sha256/" + digest[:2] + "/" + digest, nil
}
