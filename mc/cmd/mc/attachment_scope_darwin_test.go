//go:build !test_fake_routing

package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPrivateAttachmentSpineRejectsProductionHostScope(t *testing.T) {
	request := privateAttachmentIngressReserveRequest{
		OwnerGeneration: "a1b2c3d4e5f60718293a4b5c6d7e8f90",
		Session:         "h-private",
	}
	var input bytes.Buffer
	if err := writePrivateFrame(&input, request); err != nil {
		t.Fatalf("The valid attachment frame failed to encode: %v.", err)
	}
	args := []string{
		"__attachment-ingress-reserve-spine", "--deadline-unix-ms",
		strconv.FormatInt(time.Now().Add(time.Second).UnixMilli(), 10),
	}
	var stdout, stderr bytes.Buffer
	code := runPrivateAttachmentSpine(args, &input, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "requires the helper's fixed spine scope") {
		t.Fatalf("The production host scope returned an unexpected result: code=%d stdout=%q stderr=%q.",
			code, stdout.String(), stderr.String())
	}
}
