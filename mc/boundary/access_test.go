package boundary_test

import (
	"testing"

	"mc/boundary"
)

func TestResolveAccessBilateralTruthTable(t *testing.T) {
	tests := []struct {
		name      string
		requested boundary.Access
		maximum   boundary.Access
		want      boundary.Access
		wantErr   bool
	}{
		{name: "RO request under RO maximum", requested: boundary.AccessRO, maximum: boundary.AccessRO, want: boundary.AccessRO},
		{name: "RO request under RW maximum", requested: boundary.AccessRO, maximum: boundary.AccessRW, want: boundary.AccessRO},
		{name: "RW request under RW maximum", requested: boundary.AccessRW, maximum: boundary.AccessRW, want: boundary.AccessRW},
		{name: "RW request under RO maximum rejects", requested: boundary.AccessRW, maximum: boundary.AccessRO, wantErr: true},
		{name: "invalid request rejects", requested: "read-only", maximum: boundary.AccessRW, wantErr: true},
		{name: "invalid maximum rejects", requested: boundary.AccessRO, maximum: "read-write", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := boundary.ResolveAccess(tt.requested, tt.maximum)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveAccess(%q, %q) error = %v, wantErr %v", tt.requested, tt.maximum, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ResolveAccess(%q, %q) = %q, want %q", tt.requested, tt.maximum, got, tt.want)
			}
			if tt.wantErr {
				if gotCode := codeOf(t, err); gotCode != boundary.CodeRWNotPermitted {
					t.Fatalf("code = %q, want %q", gotCode, boundary.CodeRWNotPermitted)
				}
			}
		})
	}
}
