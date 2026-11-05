//go:build !linux || test_fake_routing

package verbs

func AttachmentCanaryPublish(id *RunIdentity) (AttachmentCanaryPublisherReceipt, error) {
	var out AttachmentCanaryPublisherReceipt
	return out, Usagef("attachment canary publisher is available only in the production Linux image")
}
