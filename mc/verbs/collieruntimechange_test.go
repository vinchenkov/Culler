package verbs

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	"mc/domain"
)

func collieRuntimeChangeFixture(t *testing.T) (*sql.DB, string, *RunIdentity, int64) {
	t.Helper()
	db := proxyHealth(t, "connection_access")
	const session = "h-runtime-change"
	hwSessionBound(t, db, session, "codex/profile")
	const launch = "ffffffffffffffff"
	dvExec(t, db, `UPDATE collie_sessions SET current_launch_id=?,current_launch_mode='fresh',current_container_id=?,launch_bound_at=datetime('now'),launch_started_at=datetime('now'),native_session_ref='retained-native-session',trace_filename='retained.jsonl' WHERE id=?`, launch, hbContainer, session)
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:runtime-change", Body: "Keep this message."}); err != nil {
		t.Fatal(err)
	}
	var message int64
	if err := db.QueryRow(`SELECT id FROM conversation_messages WHERE session_id=?`, session).Scan(&message); err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Tier: "collie", RunID: session, Launch: launch, VerbAllowlist: MaximumCollieAgentVerbs(), RuntimeBinding: PrivateRuntimeBinding{
		ID: "profile", Harness: "codex", Model: "fixture-model", Credential: "proxy-test", AuthorizationID: "authorization-1", ProfileRevision: "profile-revision",
	}}
	return db, session, id, message
}

func requireRuntimeChanged(t *testing.T, err error) {
	t.Helper()
	var refusal *domain.DomainError
	if !errors.As(err, &refusal) || refusal.Code != domain.CodeRuntimeChanged {
		t.Fatalf("The stale launch did not receive its refusal code: %v", err)
	}
}

func TestCollieClaimRejectsChangedRuntimeBeforeAttempt(t *testing.T) {
	for _, change := range []string{"verified profile", "unverified profile", "authorization"} {
		t.Run(change, func(t *testing.T) {
			db, session, id, message := collieRuntimeChangeFixture(t)
			switch change {
			case "verified profile":
				dvExec(t, db, `UPDATE connection_health SET profile_revision='replacement-profile' WHERE connection_id='proxy-test' AND profile_id='profile'`)
			case "unverified profile":
				dvExec(t, db, `UPDATE connection_health SET profile_revision='replacement-profile',state='unknown' WHERE connection_id='proxy-test' AND profile_id='profile'`)
			case "authorization":
				dvExec(t, db, `UPDATE connection_health SET authorization_id='replacement-authorization' WHERE connection_id='proxy-test'`)
			}
			_, err := CollieClaim(db, id, session)
			requireRuntimeChanged(t, err)
			_, err = CollieLaunchCheck(db, nil, session, id.Launch, id.RuntimeBinding.Credential, id.RuntimeBinding.AuthorizationID, id.RuntimeBinding.ID, id.RuntimeBinding.ProfileRevision)
			requireRuntimeChanged(t, err)
			var attempts int
			var claimed, completed sql.NullString
			if err := db.QueryRow(`SELECT count(*) FROM collie_reply_attempts WHERE message_id=?`, message).Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT claimed_at,completed_at FROM conversation_messages WHERE id=?`, message).Scan(&claimed, &completed); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 || claimed.Valid || completed.Valid {
				t.Fatal("The stale launch changed the pending message.")
			}
		})
	}
}

func TestCollieClaimPreservesAuthorizedRetryOnRuntimeChange(t *testing.T) {
	for _, change := range []string{"profile", "authorization"} {
		t.Run(change, func(t *testing.T) {
			db, session, id, message := collieRuntimeChangeFixture(t)
			attempt := domain.DiagnosticID()
			dvExec(t, db, `INSERT INTO collie_reply_attempts(attempt_id,session_id,message_id,launch_id,state) VALUES(?,?,?,?,'authorized')`, attempt, session, message, id.Launch)
			if change == "profile" {
				dvExec(t, db, `UPDATE connection_health SET profile_revision='replacement-profile' WHERE connection_id='proxy-test' AND profile_id='profile'`)
			} else {
				dvExec(t, db, `UPDATE connection_health SET authorization_id='replacement-authorization' WHERE connection_id='proxy-test'`)
			}
			_, err := CollieClaim(db, id, session)
			requireRuntimeChanged(t, err)
			var state, launch string
			if err := db.QueryRow(`SELECT state,launch_id FROM collie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&state, &launch); err != nil {
				t.Fatal(err)
			}
			if state != "authorized" || launch != id.Launch {
				t.Fatal("The refusal changed the authorized retry.")
			}
		})
	}
}

func TestCollieCurrentUnverifiedRuntimeDoesNotRequestReplacement(t *testing.T) {
	for _, state := range []string{"unknown", "stopped", "retry_wait"} {
		t.Run(state, func(t *testing.T) {
			db, session, id, _ := collieRuntimeChangeFixture(t)
			dvExec(t, db, `UPDATE connection_health SET state=?,next_retry_at='2999-01-01T00:00:00Z' WHERE connection_id='proxy-test' AND profile_id='profile'`, state)
			_, err := CollieClaim(db, id, session)
			var refusal *domain.DomainError
			if !errors.As(err, &refusal) || refusal.Code == domain.CodeRuntimeChanged {
				t.Fatalf("An unchanged unhealthy profile requested a replacement: %v", err)
			}
		})
	}
}

func TestCollieRuntimeRecoveryWaitsForVerificationAndPreservesContext(t *testing.T) {
	db, session, id, message := collieRuntimeChangeFixture(t)
	dvExec(t, db, `UPDATE connection_health SET profile_revision='replacement-profile',state='unknown' WHERE connection_id='proxy-test' AND profile_id='profile'`)
	_, err := CollieClaim(db, id, session)
	requireRuntimeChanged(t, err)
	if effect, handled := hwAbsentRound(t, db, 1800); !handled || effect["reason"] != "exited-owed-reply" {
		t.Fatalf("The absent launch did not recover: %+v", effect)
	}
	current := id.RuntimeBinding
	current.ProfileRevision = "replacement-profile"
	current.Model = "replacement-model"
	capability := PrivateRuntimeCapability{Digest: dvCapability(t).Digest, Bindings: []PrivateRuntimeBinding{current}}
	if effect, handled := hwWakeRoundWith(t, db, 1800, capability, []PrivateCollieContainer{}); handled {
		t.Fatalf("The unverified profile started a launch: %+v", effect)
	}
	check := ConnectionCheck{ConnectionID: current.Credential, AuthorizationID: current.AuthorizationID, ProfileID: current.ID, ProfileRevision: current.ProfileRevision, Operation: "model_access", CheckID: domain.DiagnosticID()}
	if _, err := ConnectionVerified(db, nil, ConnectionObservation{ConnectionCheck: check, EventID: domain.DiagnosticID(), Outcome: "ready", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	wake, handled := hwWakeRoundWith(t, db, 1800, capability, []PrivateCollieContainer{})
	if !handled || wake["mode"] != "native" || wake["native_session_ref"] != "retained-native-session" || wake["trace_filename"] != "retained.jsonl" {
		t.Fatalf("The verified replacement lost the native context: %+v", wake)
	}
	replacement := *id
	replacement.Launch = wake["launch"].(string)
	replacement.RuntimeBinding = current
	claim, err := CollieClaim(db, &replacement, session)
	if err != nil {
		t.Fatal(err)
	}
	got := claim.(map[string]any)["message"].(map[string]any)
	if got["id"] != strconv.FormatInt(message, 10) || got["body"] != "Keep this message." || got["reclaimed"] != false {
		t.Fatalf("The replacement lost the unclaimed message: %+v", got)
	}
}

func TestCollieActiveAttemptRetainsItsRuntimeUntilCompletion(t *testing.T) {
	db, session, id, _ := collieRuntimeChangeFixture(t)
	first, err := CollieClaim(db, id, session)
	if err != nil {
		t.Fatal(err)
	}
	attempt := first.(map[string]any)["message"].(map[string]any)["attempt_id"]
	dvExec(t, db, `UPDATE connection_health SET profile_revision='replacement-profile' WHERE connection_id='proxy-test' AND profile_id='profile'`)
	current, err := CollieClaim(db, id, session)
	if err != nil || current.(map[string]any)["message"].(map[string]any)["attempt_id"] != attempt {
		t.Fatalf("The profile change replaced an active attempt: %+v %v", current, err)
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, id.RuntimeBinding.Credential, id.RuntimeBinding.ID, time.Now(), id.RuntimeBinding.ProfileRevision); err == nil {
		t.Fatal("The active attempt test did not change the profile.")
	}
}
