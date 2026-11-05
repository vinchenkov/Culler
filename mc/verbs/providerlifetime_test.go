package verbs

import (
	"context"
	"database/sql"
	"mc/domain"
	"strings"
	"testing"
	"time"
)

func TestProviderLifetimeUsesCurrentHomieLaunchAndActivity(t *testing.T) {
	db := hbSpine(t)
	now := time.Now().UTC().Truncate(time.Second)
	dvExec(t, db, `UPDATE homie_sessions SET last_activity_at=? WHERE id='sess-1'`, now.Format(spineTime))
	dvExec(t, db, `UPDATE settings SET homie_idle_timeout_s=60 WHERE id=1`)
	clock := domain.VMClock{BootID: "fixture", Uptime: time.Hour}
	launch := ProviderLaunchIdentity{RunID: "sess-1", LaunchID: hbLaunch}
	expires, err := providerLaunchExpiry(context.Background(), db, launch, now, clock)
	if err != nil || !expires.Equal(now.Add(time.Minute)) {
		t.Fatalf("Unexpected live expiry: %v %v", expires, err)
	}
	launch.LaunchID = "old-launch"
	expires, err = providerLaunchExpiry(context.Background(), db, launch, now, clock)
	if err != nil || !expires.IsZero() {
		t.Fatalf("The old launch retained authority: %v %v", expires, err)
	}
	launch.LaunchID = hbLaunch
	dvExec(t, db, `UPDATE homie_sessions SET status='ended' WHERE id='sess-1'`)
	expires, err = providerLaunchExpiry(context.Background(), db, launch, now, clock)
	if err != nil || !expires.IsZero() {
		t.Fatalf("The ended session retained authority: %v %v", expires, err)
	}
}

func TestProviderLifetimeUsesPipelineLeaseAndCurrentVMBoot(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO runs(id,tier,role,project) VALUES('provider-run','pipeline','worker','ws-test')`)
	lease := dvHold(t, db, "ws-test", "provider-run", "worker", nil, 0, nil, time.Minute)
	clock, err := domain.ReadVMClock()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	launch := ProviderLaunchIdentity{RunID: "provider-run", LaunchID: "provider-run"}
	expires, err := providerLaunchExpiry(context.Background(), db, launch, now, clock)
	expected := now.Add(lease.DeadlineUptime - clock.Uptime.Truncate(time.Millisecond))
	if err != nil || !expires.Equal(expected) {
		t.Fatalf("Unexpected pipeline expiry: %v %v", expires, err)
	}
	for _, changed := range []domain.VMClock{{BootID: "another-boot", Uptime: clock.Uptime}, {BootID: clock.BootID, Uptime: lease.DeadlineUptime}} {
		expires, err = providerLaunchExpiry(context.Background(), db, launch, now, changed)
		if err != nil || !expires.IsZero() {
			t.Fatalf("Invalid VM state retained authority: %v %v", expires, err)
		}
	}
	dvExec(t, db, `UPDATE runs SET ended_at=datetime('now') WHERE id='provider-run'`)
	expires, err = providerLaunchExpiry(context.Background(), db, launch, now, clock)
	if err != sql.ErrNoRows || !expires.IsZero() {
		t.Fatalf("The completed run retained authority: %v %v", expires, err)
	}
}

func TestProviderLifetimeBoundsSetupAndWikiRuns(t *testing.T) {
	db := dvSpine(t)
	now := time.Now().UTC().Truncate(time.Second)
	clock, err := domain.ReadVMClock()
	if err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `INSERT INTO environment_operations(id,project_id,revision_id,kind,status,inputs_digest,image_digest,created_at)
 VALUES('provider-setup','ws-test','fixture-environment-ws-test','setup','running','digest','image',?)`, now.Format(spineTime))
	dvExec(t, db, `UPDATE project_environments SET active_operation_id='provider-setup' WHERE project_id='ws-test'`)
	launch := ProviderLaunchIdentity{RunID: "provider-setup", LaunchID: "provider-setup"}
	expires, err := providerLaunchExpiry(context.Background(), db, launch, now, clock)
	if err != nil || !expires.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("Unexpected setup deadline: %v %v", expires, err)
	}
	dvExec(t, db, `UPDATE environment_operations SET status='canceled',ended_at=datetime('now') WHERE id='provider-setup'`)
	if _, err := providerLaunchExpiry(context.Background(), db, launch, now, clock); err != sql.ErrNoRows {
		t.Fatalf("A canceled setup retained authority: %v", err)
	}
	wrActive(t, db, "ws-test", "provider-wiki", strings.Repeat("a", 40), "", 0, nil, time.Minute)
	launch = ProviderLaunchIdentity{RunID: "provider-wiki", LaunchID: "provider-wiki"}
	expires, err = providerLaunchExpiry(context.Background(), db, launch, now, clock)
	if err != nil || !expires.After(now) || expires.After(now.Add(2*time.Minute)) {
		t.Fatalf("Unexpected wiki deadline: %v %v", expires, err)
	}
}

func providerLifetimeResult(t *testing.T, db *sql.DB, operation string) ProviderLaunchLifetime {
	t.Helper()
	result, err := ProviderAccessLifetimes(db, nil, []ProviderLaunchIdentity{{RunID: operation, LaunchID: operation}})
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)["launches"].([]ProviderLaunchLifetime)[0]
}

func TestProviderLifetimePreservesSetupAuthorityBeforeFirstHeartbeat(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	if op.Kind != "setup" || op.Status != "queued" {
		t.Fatalf("The environment work did not return a queued setup: %+v", op)
	}
	created, err := parseSpineTime(op.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	deadline := created.Add(30 * time.Minute).Format(time.RFC3339Nano)
	for check := 0; check < 3; check++ {
		lifetime := providerLifetimeResult(t, db, op.ID)
		if !lifetime.Allowed || lifetime.ExpiresAt != deadline {
			t.Fatalf("The queued setup lost its fixed lifetime before its first heartbeat: %+v", lifetime)
		}
	}
	environmentCall(t, db, environmentSetupID(op), "heartbeat", EnvironmentRequest{OperationID: op.ID})
	if lifetime := providerLifetimeResult(t, db, op.ID); !lifetime.Allowed || lifetime.ExpiresAt != deadline {
		t.Fatalf("The first heartbeat changed the setup lifetime: %+v", lifetime)
	}
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{
		OperationID: op.ID, InputsDigest: op.InputsDigest, Status: "failed", Error: "The setup process stopped.",
	})
	if lifetime := providerLifetimeResult(t, db, op.ID); lifetime.Allowed || lifetime.ExpiresAt != "" {
		t.Fatalf("The failed setup retained provider access: %+v", lifetime)
	}
	dvExec(t, db, `UPDATE environment_operations SET retry_at=NULL`)
	retry := environmentWork(t, db)[0]
	if retry.ID == op.ID || retry.Status != "queued" || !providerLifetimeResult(t, db, retry.ID).Allowed {
		t.Fatal("The queued retry did not receive its own provider lifetime.")
	}
	if providerLifetimeResult(t, db, op.ID).Allowed {
		t.Fatal("The retry restored authority to the failed setup.")
	}
}

func TestProviderLifetimeBoundsEnvironmentAuthority(t *testing.T) {
	for _, kind := range []string{"setup", "validate", "build", "accept", "cache-clear", "runtime-check"} {
		for _, status := range []string{"queued", "running", "succeeded", "failed", "canceled"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				db := dvSpine(t)
				created := time.Now().UTC().Truncate(time.Second).Add(-29 * time.Minute)
				var ended any
				if status != "queued" && status != "running" {
					ended = created.Add(time.Minute).Format(spineTime)
				}
				dvExec(t, db, `INSERT INTO environment_operations(id,project_id,revision_id,kind,status,inputs_digest,image_digest,created_at,ended_at)
 VALUES('provider-environment','ws-test','fixture-environment-ws-test',?,?,'digest','image',?,?)`, kind, status, created.Format(spineTime), ended)
				dvExec(t, db, `UPDATE project_environments SET active_operation_id='provider-environment' WHERE project_id='ws-test'`)
				allowed := (kind == "setup" || kind == "validate") && (status == "queued" || status == "running")
				lifetime := providerLifetimeResult(t, db, "provider-environment")
				if lifetime.Allowed != allowed {
					t.Fatalf("Unexpected provider authority: %+v", lifetime)
				}
				if !allowed {
					if lifetime.ExpiresAt != "" {
						t.Fatal("The refused environment operation retained a deadline.")
					}
					return
				}
				if lifetime.ExpiresAt != created.Add(30*time.Minute).Format(time.RFC3339Nano) {
					t.Fatal("The provider lifetime extended the fixed environment deadline.")
				}
				for _, condition := range []string{"different-launch", "not-current", "expired", "archived"} {
					t.Run(condition, func(t *testing.T) {
						launch := ProviderLaunchIdentity{RunID: "provider-environment", LaunchID: "provider-environment"}
						switch condition {
						case "different-launch":
							launch.LaunchID = "another-launch"
						case "not-current":
							dvExec(t, db, `UPDATE project_environments SET active_operation_id=NULL WHERE project_id='ws-test'`)
							defer dvExec(t, db, `UPDATE project_environments SET active_operation_id='provider-environment' WHERE project_id='ws-test'`)
						case "expired":
							dvExec(t, db, `UPDATE environment_operations SET created_at=? WHERE id='provider-environment'`, time.Now().Add(-31*time.Minute).UTC().Format(spineTime))
							defer dvExec(t, db, `UPDATE environment_operations SET created_at=? WHERE id='provider-environment'`, created.Format(spineTime))
						case "archived":
							dvExec(t, db, `UPDATE projects SET status='archived' WHERE id='ws-test'`)
						}
						result, err := ProviderAccessLifetimes(db, nil, []ProviderLaunchIdentity{launch})
						if err != nil {
							t.Fatal(err)
						}
						refused := result.(map[string]any)["launches"].([]ProviderLaunchLifetime)[0]
						if refused.Allowed || refused.ExpiresAt != "" {
							t.Fatalf("The environment operation retained provider authority: %+v", refused)
						}
					})
				}
			})
		}
	}
}
