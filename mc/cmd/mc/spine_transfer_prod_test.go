//go:build !test_fake_routing

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProductionResetRequiresConfirmationBeforeAnyRuntimeProbe(t *testing.T) {
	for name, args := range map[string][]string{
		"missing":    {"__reset"},
		"unexpected": {"__reset", "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := brokerReset(args, &stdout, &stderr)
			if name == "missing" && (code != 1 || !strings.Contains(stderr.String(), "--confirm")) {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			if name == "unexpected" && code != 2 {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			var envelope map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || envelope["error"] == nil {
				t.Fatalf("envelope=%v err=%v", envelope, err)
			}
		})
	}
}
