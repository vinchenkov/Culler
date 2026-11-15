package boundary

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// suffixCaseMode is the case rule of the volume containing an absent root's
// nearest existing canonical ancestor. It applies only to unresolved suffix
// components; the ancestor half remains filesystem identity.
type suffixCaseMode uint8

const (
	suffixCaseUnknown suffixCaseMode = iota
	suffixCaseSensitive
	suffixCaseInsensitive
)

const (
	volumeCapabilitiesFormat   = 0
	volumeCapFmtCaseSensitive  = uint32(0x00000100)
	volumeCapabilityBufferSize = uint32(36) // Darwin ABI: uint32 + 4 words + 4 words
)

// volumeCapabilityWords is the fixed ATTR_VOL_CAPABILITIES payload returned by
// getattrlist(2). Keeping parsing platform-neutral lets short/invalid kernel
// results be tested without a Darwin syscall seam.
type volumeCapabilityWords struct {
	Length       uint32
	Capabilities [4]uint32
	Valid        [4]uint32
}

func parseVolumeCaseCapabilities(words volumeCapabilityWords) (suffixCaseMode, error) {
	if words.Length < volumeCapabilityBufferSize {
		return suffixCaseUnknown, mountErrf(CodeDeniedRoot,
			"volume capability payload is %d bytes, want at least %d; case behavior is unknown",
			words.Length, volumeCapabilityBufferSize)
	}
	valid := words.Valid[volumeCapabilitiesFormat]
	if valid&volumeCapFmtCaseSensitive == 0 {
		return suffixCaseUnknown, mountErrf(CodeDeniedRoot,
			"volume did not validate its case-sensitivity capability; case behavior is unknown")
	}
	if words.Capabilities[volumeCapabilitiesFormat]&volumeCapFmtCaseSensitive != 0 {
		return suffixCaseSensitive, nil
	}
	return suffixCaseInsensitive, nil
}

// suffixesOverlap reports whether either suffix is a whole-component prefix of
// the other. That is the absent-root form of bidirectional intersection: the
// source may be an ancestor or a descendant of the would-be protected path.
func suffixesOverlap(protected, source []string, mode suffixCaseMode) (bool, error) {
	limit := min(len(protected), len(source))
	var caseAmbiguity error
	for i := 0; i < limit; i++ {
		equal, err := suffixComponentEqual(protected[i], source[i], mode)
		if err != nil {
			// Unknown case semantics make this component conditional, not the
			// whole path immediately ambiguous. A later component may differ
			// under both case modes, proving non-overlap regardless of this one.
			// Defer the error until the complete candidate prefix has matched.
			if caseAmbiguity == nil {
				caseAmbiguity = err
			}
			continue
		}
		if !equal {
			return false, nil
		}
	}
	if caseAmbiguity != nil {
		return false, caseAmbiguity
	}
	return true, nil
}

func suffixComponentEqual(a, b string, mode suffixCaseMode) (bool, error) {
	a = norm.NFC.String(a)
	b = norm.NFC.String(b)
	if a == b {
		return true, nil
	}

	// cases.Fold performs full Unicode folding, including multi-rune expansions
	// such as ß -> ss. EqualFold implements simple folding and is insufficient.
	fold := cases.Fold()
	aFolded := norm.NFC.String(fold.String(a))
	bFolded := norm.NFC.String(fold.String(b))
	if aFolded != bFolded {
		return false, nil // distinct under either volume mode
	}

	switch mode {
	case suffixCaseSensitive:
		return false, nil
	case suffixCaseInsensitive:
		return true, nil
	default:
		return false, mountErrf(CodeDeniedRoot,
			"volume case sensitivity is unknown for fold-equivalent suffix components %q and %q; ambiguity denies",
			a, b)
	}
}
