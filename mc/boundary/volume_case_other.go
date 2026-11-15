//go:build !darwin

package boundary

// No portable syscall reports per-volume case semantics. Unknown remains
// useful: exact NFC matches and different full folds are still decidable; only
// a fold-equivalent variant fails closed in suffixComponentEqual.
func suffixCaseModeForPath(string) (suffixCaseMode, error) {
	return suffixCaseUnknown, nil
}
