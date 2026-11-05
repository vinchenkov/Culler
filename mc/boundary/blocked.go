package boundary

import (
	"fmt"
	"strings"
)

const (
	maxAdditionalBlockedPatterns = 128
	maxBlockedPatternBytes       = 255
)

// PatternKind is one closed blocked-address pattern language.
type PatternKind string

const (
	PatternComponent    PatternKind = "component"
	PatternBasenameGlob PatternKind = "basename-glob"
)

// BlockPattern is one operator-added deny. The shipped patterns are private
// and are always evaluated, including by the zero-value BlockPolicy.
type BlockPattern struct {
	Kind    PatternKind
	Pattern string
}

var shippedBlockedPatterns = [...]BlockPattern{
	{Kind: PatternComponent, Pattern: ".ssh"},
	{Kind: PatternComponent, Pattern: ".aws"},
	{Kind: PatternComponent, Pattern: ".azure"},
	{Kind: PatternComponent, Pattern: ".config"},
	{Kind: PatternComponent, Pattern: ".docker"},
	{Kind: PatternComponent, Pattern: ".gnupg"},
	{Kind: PatternComponent, Pattern: ".kube"},
	{Kind: PatternComponent, Pattern: ".codex"},
	{Kind: PatternComponent, Pattern: ".claude"},
	{Kind: PatternComponent, Pattern: ".env"},
	{Kind: PatternComponent, Pattern: ".netrc"},
	{Kind: PatternComponent, Pattern: ".npmrc"},
	{Kind: PatternComponent, Pattern: ".pypirc"},
	{Kind: PatternComponent, Pattern: ".git-credentials"},
	{Kind: PatternComponent, Pattern: "credentials"},
	{Kind: PatternComponent, Pattern: "secrets"},
	{Kind: PatternComponent, Pattern: "keychains"},
	{Kind: PatternComponent, Pattern: "kubeconfig"},
	{Kind: PatternBasenameGlob, Pattern: ".env.*"},
	{Kind: PatternBasenameGlob, Pattern: "*credentials*.json"},
	{Kind: PatternBasenameGlob, Pattern: "auth.json"},
	{Kind: PatternBasenameGlob, Pattern: "id_rsa*"},
	{Kind: PatternBasenameGlob, Pattern: "id_dsa*"},
	{Kind: PatternBasenameGlob, Pattern: "id_ecdsa*"},
	{Kind: PatternBasenameGlob, Pattern: "id_ed25519*"},
	{Kind: PatternBasenameGlob, Pattern: "private_key*"},
	{Kind: PatternBasenameGlob, Pattern: "private-key*"},
	{Kind: PatternBasenameGlob, Pattern: "*private_key*"},
	{Kind: PatternBasenameGlob, Pattern: "*private-key*"},
	{Kind: PatternBasenameGlob, Pattern: "*service_account*.json"},
	{Kind: PatternBasenameGlob, Pattern: "*service-account*.json"},
	{Kind: PatternBasenameGlob, Pattern: "*.pem"},
	{Kind: PatternBasenameGlob, Pattern: "*.key"},
	{Kind: PatternBasenameGlob, Pattern: "*.p12"},
	{Kind: PatternBasenameGlob, Pattern: "*.pfx"},
	{Kind: PatternBasenameGlob, Pattern: "*.ppk"},
	{Kind: PatternBasenameGlob, Pattern: "*.jks"},
	{Kind: PatternBasenameGlob, Pattern: "*.keystore"},
	{Kind: PatternBasenameGlob, Pattern: "*.kdbx"},
	{Kind: PatternBasenameGlob, Pattern: "*.keychain-db"},
}

// BlockPolicy contains only validated additions. The non-removable shipped
// floor is compiled into Rejects rather than stored on the value, so even a
// zero-value policy cannot omit it.
type BlockPolicy struct {
	additions []BlockPattern
}

// NewBlockPolicy validates the additive operator extension. It has no API for
// replacement, deletion, or negation of the shipped floor.
func NewBlockPolicy(additions []BlockPattern) (BlockPolicy, error) {
	if len(additions) > maxAdditionalBlockedPatterns {
		return BlockPolicy{}, fmt.Errorf("blocked policy: %d additions exceeds %d-pattern limit", len(additions), maxAdditionalBlockedPatterns)
	}
	validated := make([]BlockPattern, len(additions))
	for i, addition := range additions {
		if err := validateBlockPattern(addition); err != nil {
			return BlockPolicy{}, fmt.Errorf("blocked policy addition[%d]: %w", i, err)
		}
		addition.Pattern = asciiLower(addition.Pattern)
		validated[i] = addition
	}
	return BlockPolicy{additions: validated}, nil
}

// Rejects reports whether either the raw-clean or resolved source address has
// a component denied by the immutable floor or the validated additions. It
// classifies only address components; it never scans directory contents.
func (p BlockPolicy) Rejects(rawClean, resolved string) bool {
	return p.rejectsOne(rawClean) || p.rejectsOne(resolved)
}

func (p BlockPolicy) rejectsOne(source string) bool {
	for _, component := range strings.Split(source, "/") {
		component = asciiLower(component)
		for _, pattern := range shippedBlockedPatterns {
			if blockPatternMatches(pattern, component) {
				return true
			}
		}
		for _, pattern := range p.additions {
			if blockPatternMatches(pattern, component) {
				return true
			}
		}
	}
	return false
}

func validateBlockPattern(pattern BlockPattern) error {
	if pattern.Pattern == "" {
		return fmt.Errorf("pattern must not be empty")
	}
	if len(pattern.Pattern) > maxBlockedPatternBytes {
		return fmt.Errorf("pattern exceeds %d ASCII bytes", maxBlockedPatternBytes)
	}
	for i := 0; i < len(pattern.Pattern); i++ {
		b := pattern.Pattern[i]
		if b < 0x20 || b >= 0x7f {
			return fmt.Errorf("pattern must contain printable ASCII only")
		}
	}

	switch pattern.Kind {
	case PatternComponent:
		if strings.ContainsAny(pattern.Pattern, `/\\*?[]`) {
			return fmt.Errorf("component pattern must be one literal component without separator or wildcard syntax")
		}
	case PatternBasenameGlob:
		if strings.ContainsAny(pattern.Pattern, `/\\?[]`) {
			return fmt.Errorf("basename-glob permits literals and '*' only, without separators")
		}
		for i := 0; i < len(pattern.Pattern); i++ {
			if pattern.Pattern[i] >= 'A' && pattern.Pattern[i] <= 'Z' {
				return fmt.Errorf("basename-glob must be lowercase ASCII")
			}
		}
	default:
		return fmt.Errorf("unknown pattern kind %q", pattern.Kind)
	}
	return nil
}

func blockPatternMatches(pattern BlockPattern, component string) bool {
	if pattern.Kind == PatternComponent {
		return component == pattern.Pattern
	}
	return matchStarGlob(pattern.Pattern, component)
}

// matchStarGlob implements the entire basename-glob language: literal bytes
// plus '*'. Validation keeps every richer glob construct out of this matcher.
func matchStarGlob(pattern, value string) bool {
	patternIndex, valueIndex := 0, 0
	starIndex, retryValue := -1, 0
	for valueIndex < len(value) {
		if patternIndex < len(pattern) && pattern[patternIndex] != '*' && pattern[patternIndex] == value[valueIndex] {
			patternIndex++
			valueIndex++
			continue
		}
		if patternIndex < len(pattern) && pattern[patternIndex] == '*' {
			starIndex = patternIndex
			patternIndex++
			retryValue = valueIndex
			continue
		}
		if starIndex >= 0 {
			patternIndex = starIndex + 1
			retryValue++
			valueIndex = retryValue
			continue
		}
		return false
	}
	for patternIndex < len(pattern) && pattern[patternIndex] == '*' {
		patternIndex++
	}
	return patternIndex == len(pattern)
}

func asciiLower(value string) string {
	var lowered strings.Builder
	lowered.Grow(len(value))
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		lowered.WriteByte(b)
	}
	return lowered.String()
}
