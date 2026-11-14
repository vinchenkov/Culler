package verbs

import "testing"

func TestRunJSONPathPrivilegedInvocationCannotRedirectIdentity(t *testing.T) {
	if got := runJSONPathForCredentials("linux", 10002, 10001, "/tmp/forged.json"); got != "/mc/run.json" {
		t.Fatalf("setuid path = %q, want fixed /mc/run.json", got)
	}
	if got := runJSONPathForCredentials("linux", 10002, 10001, ""); got != "/mc/run.json" {
		t.Fatalf("setuid empty override path = %q, want fixed /mc/run.json", got)
	}
}

func TestRunJSONPathDirectInvocationKeepsTestSeam(t *testing.T) {
	if got := runJSONPathForCredentials("darwin", 501, 501, "/tmp/test-run.json"); got != "/tmp/test-run.json" {
		t.Fatalf("direct path = %q, want test override", got)
	}
	if got := runJSONPathForCredentials("linux", 10002, 10002, ""); got != "/mc/run.json" {
		t.Fatalf("direct default path = %q, want /mc/run.json", got)
	}
}
