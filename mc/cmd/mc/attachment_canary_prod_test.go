//go:build !test_fake_routing

package main

import (
	"testing"

	"mc/verbs"
)

func TestValidateAttachmentCanaryGateReportRefusesWidening(t *testing.T) {
	green := attachmentCanaryGateReport{
		IsDirectory: true,
		UID:         10001,
		GID:         10001,
		Mode:        0o700,
		Traverse:    "EACCES",
	}
	if err := validateAttachmentCanaryGateReport(green); err != nil {
		t.Fatalf("green private gate = %v", err)
	}
	for name, mutate := range map[string]func(*attachmentCanaryGateReport){
		"wrong kind": func(r *attachmentCanaryGateReport) { r.IsDirectory = false },
		"wrong uid":  func(r *attachmentCanaryGateReport) { r.UID = 0 },
		"wrong gid":  func(r *attachmentCanaryGateReport) { r.GID = 0 },
		"widened":    func(r *attachmentCanaryGateReport) { r.Mode = 0o755 },
		"traversable": func(r *attachmentCanaryGateReport) {
			r.Traverse = "OK"
		},
	} {
		t.Run(name, func(t *testing.T) {
			report := green
			mutate(&report)
			if err := validateAttachmentCanaryGateReport(report); err == nil {
				t.Fatalf("private gate report %#v was accepted", report)
			}
		})
	}
}

// Docker Desktop reports the SAME bind two ways — the host path, or that path
// under /host_mnt, the VM's mount point for shared host storage — and which one
// comes back is not stable. A raw comparison therefore refuses a correct canary
// and, because the canary gates supervision activation, refuses the whole
// deployment. The identical defect killed the Homie tier outright
// (resident/src/homie-envelope.ts). Nothing above this check had a test,
// because inspectAttachmentCanary reaches Docker on its first line.
func TestAttachmentCanaryAcceptsDockerDesktopHostMntSources(t *testing.T) {
	tree := verbs.AttachmentTreeReceipt{
		Session:          "h-abc",
		InboundPublished: "/Users/op/.culler/attachments/h-abc/in/published",
		Outbound:         "/Users/op/.culler/attachments/h-abc/out",
	}
	frameDir := "/Users/op/.culler/runs/attachment-canary/gen"
	plain := []attachmentCanaryMount{
		{Type: "bind", Source: tree.InboundPublished, Destination: "/mc/attachments/in"},
		{Type: "bind", Source: tree.Outbound, Destination: "/mc/private/attachments/out", RW: true},
		{Type: "bind", Source: frameDir, Destination: "/mc/attachment-canary"},
	}
	if err := checkAttachmentCanaryMounts(plain, tree, frameDir); err != nil {
		t.Fatalf("plain host paths must pass: %v", err)
	}

	prefixed := make([]attachmentCanaryMount, len(plain))
	for i, mount := range plain {
		mount.Source = "/host_mnt" + mount.Source
		prefixed[i] = mount
	}
	if err := checkAttachmentCanaryMounts(prefixed, tree, frameDir); err != nil {
		t.Fatalf("/host_mnt spelling of the same directories must pass: %v", err)
	}

	// Docker reported it per mount, not all-or-nothing: one wake refused at one
	// destination and the next at another, so the mixed shape is the real one.
	mixed := append([]attachmentCanaryMount(nil), plain...)
	mixed[1].Source = "/host_mnt" + mixed[1].Source
	if err := checkAttachmentCanaryMounts(mixed, tree, frameDir); err != nil {
		t.Fatalf("a per-mount mixture must pass: %v", err)
	}
}

func TestAttachmentCanaryHostMntCollapseWidensNothingPastThePrefix(t *testing.T) {
	tree := verbs.AttachmentTreeReceipt{
		Session:          "h-abc",
		InboundPublished: "/Users/op/in",
		Outbound:         "/Users/op/out",
	}
	frameDir := "/Users/op/frame"
	base := []attachmentCanaryMount{
		{Type: "bind", Source: tree.InboundPublished, Destination: "/mc/attachments/in"},
		{Type: "bind", Source: tree.Outbound, Destination: "/mc/private/attachments/out", RW: true},
		{Type: "bind", Source: frameDir, Destination: "/mc/attachment-canary"},
	}
	for _, substituted := range []string{
		"/host_mnt/Users/op/substituted", // prefixed, but a different directory
		"/host_mntUsers/op/in",           // lookalike: no separator, so nothing is stripped
		"/Users/op/substituted",
	} {
		drifted := append([]attachmentCanaryMount(nil), base...)
		drifted[0].Source = substituted
		if err := checkAttachmentCanaryMounts(drifted, tree, frameDir); err == nil {
			t.Fatalf("source %q must still be refused", substituted)
		}
	}
	// The access bit is untouched by the collapse.
	widened := append([]attachmentCanaryMount(nil), base...)
	widened[0].RW = true
	if err := checkAttachmentCanaryMounts(widened, tree, frameDir); err == nil {
		t.Fatal("a read-write inbound mount must still be refused")
	}
}
