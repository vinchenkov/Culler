//go:build darwin

package boundary

import (
	"path/filepath"
	"testing"
)

func TestSuffixCaseModeForPathReportsKnownDarwinVolume(t *testing.T) {
	mode, err := suffixCaseModeForPath(t.TempDir())
	if err != nil {
		t.Fatalf("suffixCaseModeForPath(temp volume) = %v", err)
	}
	if mode != suffixCaseSensitive && mode != suffixCaseInsensitive {
		t.Fatalf("suffixCaseModeForPath(temp volume) = %v, want known mode", mode)
	}
}

func TestSuffixCaseModeForPathFailsClosedOnDarwinLookupError(t *testing.T) {
	mode, err := suffixCaseModeForPath(filepath.Join(t.TempDir(), "absent"))
	if err == nil || mode != suffixCaseUnknown {
		t.Fatalf("suffixCaseModeForPath(absent) = (%v, %v), want (unknown, error)", mode, err)
	}
}
