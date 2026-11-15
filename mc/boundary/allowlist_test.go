package boundary_test

import (
	"fmt"
	"strings"
	"testing"

	"mc/boundary"
)

func TestParseMountAllowlistAcceptsStrictSchema(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantEntries []boundary.AllowEntry
	}{
		{
			name:  "deny all",
			input: "version = 1\n",
		},
		{
			name: "ordinary entries and TOML strings",
			input: `# operator-owned policy
version = 1

[[allow]]
path = "/Users/operator/src/acme"
target = "acme"
access = "rw"

[[allow]]
path = '/Volumes/ref:one/$literal*'
target = "reference/\u03b1"
access = "ro"
`,
			wantEntries: []boundary.AllowEntry{
				{Path: "/Users/operator/src/acme", Target: "acme", Maximum: boundary.AccessRW},
				{Path: "/Volumes/ref:one/$literal*", Target: "reference/α", Maximum: boundary.AccessRO},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := boundary.ParseMountAllowlist([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseMountAllowlist() error = %v", err)
			}
			entries := got.Entries()
			if fmt.Sprint(entries) != fmt.Sprint(tt.wantEntries) {
				t.Fatalf("Entries() = %#v, want %#v", entries, tt.wantEntries)
			}
		})
	}
}

func TestMountAllowlistEntriesReturnsDefensiveCopy(t *testing.T) {
	policy, err := boundary.ParseMountAllowlist([]byte(`version = 1
[[allow]]
path = "/safe"
target = "safe"
access = "ro"
`))
	if err != nil {
		t.Fatal(err)
	}

	first := policy.Entries()
	first[0].Path = "/mutated"
	if got := policy.Entries()[0].Path; got != "/safe" {
		t.Fatalf("Entries exposed mutable policy state: got path %q", got)
	}
}

func TestParseMountAllowlistRejectsInvalidSchema(t *testing.T) {
	longPath := "/" + strings.Repeat("a", 4096)
	invalidUTF8 := string([]byte{'/', 0xff})
	tests := []struct {
		name  string
		input string
	}{
		{name: "missing version", input: "# empty policy\n"},
		{name: "duplicate version", input: "version = 1\nversion = 1\n"},
		{name: "non integer version", input: "version = 1.0\n"},
		{name: "unsupported version", input: "version = 2\n"},
		{name: "malformed TOML", input: "version = [\n"},
		{name: "trailing invalid syntax", input: "version = 1\nthis is not toml\n"},
		{name: "unknown root key", input: "version = 1\nextra = true\n"},
		{name: "case variant root key", input: "Version = 1\n"},
		{name: "unknown root table", input: "version = 1\n[extra]\nvalue = 1\n"},
		{name: "ordinary allow table", input: "version = 1\n[allow]\npath = '/safe'\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "inline allow array", input: "version = 1\nallow = [{ path = '/safe', target = 'safe', access = 'ro' }]\n"},
		{name: "dotted allow keys", input: "version = 1\nallow.path = '/safe'\nallow.target = 'safe'\nallow.access = 'ro'\n"},
		{name: "nested allow table", input: "version = 1\n[[allow]]\npath = '/safe'\ntarget = 'safe'\naccess = 'ro'\n[allow.extra]\nvalue = 1\n"},
		{name: "unknown allow key", input: "version = 1\n[[allow]]\npath = '/safe'\ntarget = 'safe'\naccess = 'ro'\nmode = 'ro'\n"},
		{name: "case variant allow key", input: "version = 1\n[[allow]]\nPath = '/safe'\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "duplicate allow path", input: "version = 1\n[[allow]]\npath = '/safe'\npath = '/other'\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "missing path", input: "version = 1\n[[allow]]\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "missing target", input: "version = 1\n[[allow]]\npath = '/safe'\naccess = 'ro'\n"},
		{name: "missing access", input: "version = 1\n[[allow]]\npath = '/safe'\ntarget = 'safe'\n"},
		{name: "path wrong type", input: "version = 1\n[[allow]]\npath = 1\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "target wrong type", input: "version = 1\n[[allow]]\npath = '/safe'\ntarget = []\naccess = 'ro'\n"},
		{name: "access wrong type", input: "version = 1\n[[allow]]\npath = '/safe'\ntarget = 'safe'\naccess = true\n"},
		{name: "empty path", input: "version = 1\n[[allow]]\npath = ''\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "relative path", input: "version = 1\n[[allow]]\npath = 'relative'\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "path newline", input: "version = 1\n[[allow]]\npath = \"/safe\\nother\"\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "path NUL", input: "version = 1\n[[allow]]\npath = \"/safe\\u0000other\"\ntarget = 'safe'\naccess = 'ro'\n"},
		// The document-level UTF-8 gate (not the per-entry path check) is what
		// rejects this; the per-entry check is an unreachable redundant guard.
		{name: "document invalid UTF-8 carried by path", input: "version = 1\n[[allow]]\npath = '" + invalidUTF8 + "'\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "path over 4096 bytes", input: "version = 1\n[[allow]]\npath = '" + longPath + "'\ntarget = 'safe'\naccess = 'ro'\n"},
		{name: "empty target", input: "version = 1\n[[allow]]\npath = '/safe'\ntarget = ''\naccess = 'ro'\n"},
		{name: "invalid target", input: "version = 1\n[[allow]]\npath = '/safe'\ntarget = '../safe'\naccess = 'ro'\n"},
		{name: "uppercase access", input: "version = 1\n[[allow]]\npath = '/safe'\ntarget = 'safe'\naccess = 'RO'\n"},
		{name: "unknown access", input: "version = 1\n[[allow]]\npath = '/safe'\ntarget = 'safe'\naccess = 'read-only'\n"},
		{name: "equal targets", input: allowlistWithTargets("docs", "docs")},
		{name: "ancestor targets", input: allowlistWithTargets("docs/api", "docs")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := boundary.ParseMountAllowlist([]byte(tt.input))
			if err == nil {
				t.Fatal("ParseMountAllowlist() error = nil, want rejection")
			}
			if got := codeOf(t, err); got != boundary.CodeAllowlistInvalid {
				t.Fatalf("code = %q, want %q", got, boundary.CodeAllowlistInvalid)
			}
		})
	}
}

func TestParseMountAllowlistEnforcesClosedBounds(t *testing.T) {
	t.Run("path exactly 4096 bytes", func(t *testing.T) {
		path := "/" + strings.Repeat("a", 4095)
		input := fmt.Sprintf("version = 1\n[[allow]]\npath = %q\ntarget = 'safe'\naccess = 'ro'\n", path)
		if _, err := boundary.ParseMountAllowlist([]byte(input)); err != nil {
			t.Fatalf("maximum path length rejected: %v", err)
		}
	})

	t.Run("file exactly 256 KiB", func(t *testing.T) {
		input := []byte("version = 1\n#")
		input = append(input, []byte(strings.Repeat("x", 256*1024-len(input)))...)
		if _, err := boundary.ParseMountAllowlist(input); err != nil {
			t.Fatalf("exact size rejected: %v", err)
		}
	})

	t.Run("file over 256 KiB", func(t *testing.T) {
		input := []byte("version = 1\n#")
		input = append(input, []byte(strings.Repeat("x", 256*1024+1-len(input)))...)
		if _, err := boundary.ParseMountAllowlist(input); err == nil {
			t.Fatal("oversize policy accepted")
		} else if got := codeOf(t, err); got != boundary.CodeAllowlistInvalid {
			t.Fatalf("code = %q, want %q", got, boundary.CodeAllowlistInvalid)
		}
	})

	for _, n := range []int{256, 257} {
		t.Run(fmt.Sprintf("%d entries", n), func(t *testing.T) {
			var input strings.Builder
			input.WriteString("version = 1\n")
			for i := range n {
				fmt.Fprintf(&input, "[[allow]]\npath = '/safe/%d'\ntarget = 'target/%d'\naccess = 'ro'\n", i, i)
			}
			_, err := boundary.ParseMountAllowlist([]byte(input.String()))
			if n == 256 && err != nil {
				t.Fatalf("maximum entry count rejected: %v", err)
			}
			if n == 257 && err == nil {
				t.Fatal("entry count over maximum accepted")
			}
			if n == 257 {
				if got := codeOf(t, err); got != boundary.CodeAllowlistInvalid {
					t.Fatalf("code = %q, want %q", got, boundary.CodeAllowlistInvalid)
				}
			}
		})
	}
}

func TestValidateTarget(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr bool
	}{
		{name: "one component", target: "a"},
		{name: "nested", target: "reference/library"},
		{name: "unicode", target: "reference/β"},
		{name: "maximum component", target: strings.Repeat("a", 255)},
		{name: "maximum total", target: strings.Repeat("a", 204) + "/" + strings.Repeat("b", 204) + "/" + strings.Repeat("c", 204) + "/" + strings.Repeat("d", 204) + "/" + strings.Repeat("e", 204)},
		{name: "empty", target: "", wantErr: true},
		{name: "absolute", target: "/reference", wantErr: true},
		{name: "trailing slash", target: "reference/", wantErr: true},
		{name: "repeated slash", target: "reference//library", wantErr: true},
		{name: "dot component", target: "reference/./library", wantErr: true},
		{name: "dot dot component", target: "reference/../library", wantErr: true},
		{name: "colon", target: "reference:ro", wantErr: true},
		{name: "backslash", target: `reference\library`, wantErr: true},
		{name: "NUL", target: "reference\x00library", wantErr: true},
		{name: "newline", target: "reference\nlibrary", wantErr: true},
		{name: "DEL", target: "reference\x7flibrary", wantErr: true},
		{name: "component over 255 bytes", target: strings.Repeat("a", 256), wantErr: true},
		{name: "total over 1024 bytes", target: strings.Repeat("a", 205) + "/" + strings.Repeat("b", 205) + "/" + strings.Repeat("c", 205) + "/" + strings.Repeat("d", 205) + "/" + strings.Repeat("e", 205), wantErr: true},
		{name: "total exactly 1025 bytes", target: strings.Repeat("a", 204) + "/" + strings.Repeat("b", 204) + "/" + strings.Repeat("c", 204) + "/" + strings.Repeat("d", 204) + "/" + strings.Repeat("e", 205), wantErr: true},
		{name: "invalid UTF-8", target: string([]byte{0xff}), wantErr: true},
		// A control character has no qualification: C1 and the Unicode line
		// and paragraph separators are line breaks to the serializers that
		// render these targets.
		{name: "C1 NEL", target: "referencelibrary", wantErr: true},
		{name: "line separator", target: "reference library", wantErr: true},
		{name: "paragraph separator", target: "reference library", wantErr: true},
		{name: "zero width space", target: "reference​library", wantErr: true},
		{name: "right-to-left override", target: "reference‮library", wantErr: true},
		{name: "non-breaking space stays legal", target: "reference library"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := boundary.ValidateTarget(tt.target)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateTarget(%q) error = %v, wantErr %v", tt.target, err, tt.wantErr)
			}
			if tt.wantErr {
				if got := codeOf(t, err); got != boundary.CodeTargetInvalid {
					t.Fatalf("code = %q, want %q", got, boundary.CodeTargetInvalid)
				}
			}
		})
	}
}

func TestParseMountAllowlistTargetCollisionSemantics(t *testing.T) {
	tests := []struct {
		name    string
		first   string
		second  string
		wantErr bool
	}{
		{name: "byte equal", first: "docs", second: "docs", wantErr: true},
		{name: "first ancestor", first: "docs", second: "docs/api", wantErr: true},
		{name: "second ancestor", first: "docs/api", second: "docs", wantErr: true},
		{name: "case distinct", first: "docs", second: "Docs"},
		{name: "prefix only", first: "doc", second: "docs"},
		{name: "nested prefix only", first: "foo/bar", second: "foo/barn"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := boundary.ParseMountAllowlist([]byte(allowlistWithTargets(tt.first, tt.second)))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseMountAllowlist() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if got := codeOf(t, err); got != boundary.CodeAllowlistInvalid {
					t.Fatalf("code = %q, want %q", got, boundary.CodeAllowlistInvalid)
				}
			}
		})
	}

	t.Run("lexical neighbor cannot hide ancestor collision", func(t *testing.T) {
		_, err := boundary.ParseMountAllowlist([]byte(allowlistWithTargets("docs", "docs-api", "docs/api")))
		if err == nil {
			t.Fatal("interleaved ancestor targets accepted")
		}
		if got := codeOf(t, err); got != boundary.CodeAllowlistInvalid {
			t.Fatalf("code = %q, want %q", got, boundary.CodeAllowlistInvalid)
		}
	})
}

func allowlistWithTargets(targets ...string) string {
	var input strings.Builder
	input.WriteString("version = 1\n")
	for i, target := range targets {
		fmt.Fprintf(&input, "[[allow]]\npath = '/safe/%d'\ntarget = %q\naccess = 'ro'\n", i, target)
	}
	return input.String()
}
