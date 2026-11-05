package verbs

import (
	"strconv"
	"strings"
	"testing"
)

func TestParseAttachmentManifestSealsCanonicalBoundedMetadata(t *testing.T) {
	m, err := ParseAttachmentManifest(`[
  {"display_name":"capture.png","media_type":"image/png","size":7,"ordinal":1},
  {"ordinal":2,"size":0,"media_type":"text/plain","display_name":"notes.txt"}
]`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Canonical != `[{"ordinal":1,"size":7,"media_type":"image/png","display_name":"capture.png"},{"ordinal":2,"size":0,"media_type":"text/plain","display_name":"notes.txt"}]` || len(m.Digest) != 64 {
		t.Fatalf("sealed manifest = %#v", m)
	}
}

func TestParseAttachmentManifestRejectsOpenOrOverBudgetDeclarations(t *testing.T) {
	for _, raw := range []string{
		`[]`,
		`[{"ordinal":2,"size":0,"media_type":"text/plain","display_name":"x"}]`,
		`[{"ordinal":1,"size":268435457,"media_type":"text/plain","display_name":"x"}]`,
		`[{"ordinal":1,"size":1,"media_type":"text/plain","display_name":"../x"}]`,
		`[{"ordinal":1,"size":1,"media_type":"text/plain","display_name":"x","path":"host"}]`,
		`[{"ordinal":1,"size":1,"media_type":"text/plain","display_name":"x"}] {}`,
	} {
		if _, err := ParseAttachmentManifest(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	entries := make([]string, 32)
	for i := range entries {
		entries[i] = `{"ordinal":` + strconv.Itoa(i+1) + `,"size":16777217,"media_type":"application/octet-stream","display_name":"x"}`
	}
	// The individual-size check above is insufficient: the aggregate must be
	// fenced before any publisher can reserve credits.
	if _, err := ParseAttachmentManifest("[" + strings.Join(entries, ",") + "]"); err == nil {
		t.Fatal("accepted an over-budget attachment batch")
	}
}
