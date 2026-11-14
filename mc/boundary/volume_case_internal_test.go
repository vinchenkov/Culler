package boundary

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestAbsentSuffixOverlapIsComponentAndVolumeAware(t *testing.T) {
	nfdCafe := norm.NFD.String("café")
	tests := []struct {
		name      string
		protected []string
		source    []string
		mode      suffixCaseMode
		want      bool
		wantErr   bool
	}{
		{name: "equal components", protected: []string{"a", "b"}, source: []string{"a", "b"}, mode: suffixCaseSensitive, want: true},
		{name: "protected is whole-component prefix", protected: []string{"a"}, source: []string{"a", "b"}, mode: suffixCaseSensitive, want: true},
		{name: "source is whole-component prefix", protected: []string{"a", "b"}, source: []string{"a"}, mode: suffixCaseSensitive, want: true},
		{name: "empty suffix is a prefix", protected: nil, source: []string{"a"}, mode: suffixCaseSensitive, want: true},
		{name: "string prefix is not a component", protected: []string{"a", "b"}, source: []string{"ab"}, mode: suffixCaseSensitive},
		{name: "component string prefix is not overlap", protected: []string{"a", "b"}, source: []string{"a", "bc"}, mode: suffixCaseSensitive},
		{name: "sensitive case variant is distinct", protected: []string{".ssh"}, source: []string{".SSH"}, mode: suffixCaseSensitive},
		{name: "insensitive case variant overlaps", protected: []string{".ssh"}, source: []string{".SSH"}, mode: suffixCaseInsensitive, want: true},
		{name: "full fold expands sharp s", protected: []string{"Straße"}, source: []string{"STRASSE"}, mode: suffixCaseInsensitive, want: true},
		{name: "full fold is not used on sensitive volume", protected: []string{"Straße"}, source: []string{"STRASSE"}, mode: suffixCaseSensitive},
		{name: "NFC and NFD overlap when sensitive", protected: []string{"café"}, source: []string{nfdCafe}, mode: suffixCaseSensitive, want: true},
		{name: "NFC and NFD overlap when insensitive", protected: []string{"CAFÉ"}, source: []string{nfdCafe}, mode: suffixCaseInsensitive, want: true},
		{name: "unknown mode still decides exact NFC", protected: []string{"café"}, source: []string{nfdCafe}, mode: suffixCaseUnknown, want: true},
		{name: "unknown mode still decides different folds", protected: []string{"alpha"}, source: []string{"beta"}, mode: suffixCaseUnknown},
		{name: "later mismatch resolves an earlier unknown case variant", protected: []string{"A", "x"}, source: []string{"a", "y"}, mode: suffixCaseUnknown},
		{name: "unknown mode denies only fold-equal variant", protected: []string{".ssh"}, source: []string{".SSH"}, mode: suffixCaseUnknown, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := suffixesOverlap(tt.protected, tt.source, tt.mode)
			if (err != nil) != tt.wantErr {
				t.Fatalf("suffixesOverlap() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("suffixesOverlap() = %v, want %v", got, tt.want)
			}
		})
	}
}
