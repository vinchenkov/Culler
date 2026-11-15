package deployment

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestConnectionOwnerSerializesOnlyTheSameConnection(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireConnectionOwner(context.Background(), home, "first")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if second, err := AcquireConnectionOwner(ctx, home, "first"); err == nil {
		second()
		t.Fatal("Two requests acquired the same connection.")
	}
	other, err := AcquireConnectionOwner(context.Background(), home, "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
}

func TestExplicitAuthorizationReplacesIdentityEvenWhenTheGrantIsUnchanged(t *testing.T) {
	home := agentProfileTestHome(t)
	grant := oauthRuntimeGrant{Credential: "test", Channel: "claude", RefreshToken: "same-fixture-grant"}
	if err := PublishAgentConnectionGrant(home, "test", grant); err != nil {
		t.Fatal(err)
	}
	first, err := AgentConnectionAuthorizationIdentity(home, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishAgentConnectionGrant(home, "test", grant); err != nil {
		t.Fatal(err)
	}
	second, err := AgentConnectionAuthorizationIdentity(home, "test")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("Explicit authorization retained the failed authorization identity.")
	}
}
