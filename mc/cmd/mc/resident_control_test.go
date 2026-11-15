package main

import (
	"strings"
	"testing"
)

func TestHelloAckMismatchDetailNamesTheFieldThatMoved(t *testing.T) {
	base := controlHello{
		Op: "hello_ack", Seq: 1, ReleaseBuildID: "abc123",
		ControlVersion: 1, SpineSchemaID: "current", ConfigSchemaVersion: 1,
		DeploymentUUID: "11111111-2222-3333-4444-555555555555",
	}
	for name, tc := range map[string]struct {
		mutate func(*controlHello)
		want   string
	}{
		"release build": {
			func(h *controlHello) { h.ReleaseBuildID = "def456" },
			"release_build_id def456 (expected abc123)",
		},
		"control protocol": {
			func(h *controlHello) { h.ControlVersion = 2 },
			"gateway_control_version 2 (expected 1)",
		},
		"spine schema": {
			func(h *controlHello) { h.SpineSchemaID = "incompatible" },
			"spine_schema_id incompatible (expected current)",
		},
		"config schema": {
			func(h *controlHello) { h.ConfigSchemaVersion = 2 },
			"config_schema_version 2 (expected 1)",
		},
		"frame": {
			func(h *controlHello) { h.Seq = 9 },
			"frame hello_ack/9 (expected hello_ack/1)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := base
			tc.mutate(&got)
			detail := helloAckMismatchDetail(got, base)
			if !strings.Contains(detail, tc.want) {
				t.Fatalf("detail = %q, want it to contain %q", detail, tc.want)
			}
		})
	}

	// The one field reported without its values. Which field is wrong is the
	// whole diagnosis there, and a deployment identifier is not something a
	// diagnostic decides to print into a log.
	t.Run("deployment uuid names the field and never the value", func(t *testing.T) {
		got := base
		got.DeploymentUUID = "99999999-8888-7777-6666-555555555555"
		detail := helloAckMismatchDetail(got, base)
		if !strings.Contains(detail, "deployment_uuid differs") {
			t.Fatalf("detail = %q, want it to name deployment_uuid", detail)
		}
		if strings.Contains(detail, got.DeploymentUUID) || strings.Contains(detail, base.DeploymentUUID) {
			t.Fatalf("detail = %q leaks a deployment uuid", detail)
		}
	})

	// Two fields moving at once is the rebuild-under-a-live-deployment case:
	// the image and the host binary disagree about more than one thing, and
	// reporting only the first would send the operator at half the problem.
	t.Run("every differing field is reported, not the first", func(t *testing.T) {
		got := base
		got.ReleaseBuildID = "def456"
		got.SpineSchemaID = "incompatible"
		detail := helloAckMismatchDetail(got, base)
		for _, want := range []string{"release_build_id def456", "spine_schema_id incompatible"} {
			if !strings.Contains(detail, want) {
				t.Fatalf("detail = %q, want it to contain %q", detail, want)
			}
		}
	})

	// An empty detail would be worse than the bare message it replaces: it
	// would read as "mismatch: " with nothing after the colon.
	t.Run("frames that agree field by field say so", func(t *testing.T) {
		detail := helloAckMismatchDetail(base, base)
		if !strings.Contains(detail, "agree field by field") {
			t.Fatalf("detail = %q", detail)
		}
	})
}
