package main

import "bytes"

// Diagnostics from a private helper crossing are bounded so a hostile or broken
// container cannot flood the operator's terminal or a log through a refusal path.
//
// This lives in an UNTAGGED file because the spine-transfer broker needs it in
// both builds (see broker_backup.go). It was originally declared in
// doctor_prod.go, whose `!test_fake_routing` tag would otherwise force the test
// build to reimplement the crossing without the bound — which is exactly the
// kind of tag-driven divergence that made the docker_e2e lane fail silently.
const privateDoctorOutputLimit = 64 * 1024

type boundedDoctorBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedDoctorBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.Len()
	if remaining < len(p) {
		b.overflow = true
		if remaining < 0 {
			remaining = 0
		}
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return original, nil
}
