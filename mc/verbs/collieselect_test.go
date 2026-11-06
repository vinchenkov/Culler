package verbs

import (
	"reflect"
	"testing"
	"time"
)

func TestSelectCollieWake(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	idle := 30 * time.Minute
	ago := func(d time.Duration) time.Time { return now.Add(-d) }

	t.Run("a non-idle no-launch session gets a fresh wake", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{
			{SessionID: "s1", LastActivityAt: ago(time.Minute), RuntimeReady: true},
		}, now, idle)
		if d.Kind != collieWakeSpawn || d.SessionID != "s1" || d.LaunchMode != "fresh" {
			t.Fatalf("decision = %+v, want fresh spawn of s1", d)
		}
	})

	t.Run("resume debt wakes in the recorded resume mode", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{
			{SessionID: "s1", LastActivityAt: ago(time.Minute), ResumeOwed: true, ResumeMode: "rows", RuntimeReady: true},
		}, now, idle)
		if d.Kind != collieWakeSpawn || d.LaunchMode != "rows" {
			t.Fatalf("decision = %+v, want a rows-mode resume wake", d)
		}
	})

	t.Run("native resume debt wakes in native mode", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{
			{SessionID: "s1", LastActivityAt: ago(time.Minute), ResumeOwed: true, ResumeMode: "native", RuntimeReady: true},
		}, now, idle)
		if d.Kind != collieWakeSpawn || d.LaunchMode != "native" {
			t.Fatalf("decision = %+v, want a native resume wake", d)
		}
	})

	t.Run("a launched session is left alone", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{
			{SessionID: "s1", LastActivityAt: ago(time.Minute), HasLaunch: true, LaunchMode: "fresh", RuntimeReady: true},
		}, now, idle)
		if d.Kind != collieWakeNone {
			t.Fatalf("decision = %+v, want none (the live container owns the session)", d)
		}
	})

	t.Run("confirmed-absent unbound launch reuses its frozen generation", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{{
			SessionID: "s1", LastActivityAt: ago(time.Minute),
			HasLaunch: true, LaunchID: "aaaaaaaaaaaaaaaa", LaunchMode: "native",
			LaunchAbsentConfirmed: true, RuntimeReady: true,
		}}, now, idle)
		if d.Kind != collieWakeSpawn || !d.ExistingLaunch ||
			d.LaunchID != "aaaaaaaaaaaaaaaa" || d.LaunchMode != "native" {
			t.Fatalf("decision = %+v, want frozen native launch debt", d)
		}
	})

	t.Run("idle end wins over a spawn and picks the oldest idle row", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{
			{SessionID: "old-idle", LastActivityAt: ago(2 * time.Hour)},
			{SessionID: "new-idle", LastActivityAt: ago(40 * time.Minute)},
			{
				SessionID: "live", LastActivityAt: ago(time.Minute),
				HasLaunch: true, LaunchID: "aaaaaaaaaaaaaaaa", LaunchMode: "native",
				LaunchAbsentConfirmed: true, RuntimeReady: true,
			},
		}, now, idle)
		if d.Kind != collieWakeIdle || d.SessionID != "old-idle" {
			t.Fatalf("decision = %+v, want idle-end of old-idle", d)
		}
	})

	t.Run("threshold equality is idle", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{
			{SessionID: "s1", LastActivityAt: ago(idle)},
		}, now, idle)
		if d.Kind != collieWakeIdle {
			t.Fatalf("decision = %+v, want idle-end at exact threshold", d)
		}
	})

	t.Run("among eligible spawns the oldest by activity wins", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{
			{SessionID: "older", LastActivityAt: ago(10 * time.Minute), RuntimeReady: true},
			{SessionID: "newer", LastActivityAt: ago(2 * time.Minute), RuntimeReady: true},
		}, now, idle)
		if d.SessionID != "older" {
			t.Fatalf("decision = %+v, want the oldest eligible spawn", d)
		}
	})

	t.Run("nothing to do returns none", func(t *testing.T) {
		if d := selectCollieWake(nil, now, idle); d.Kind != collieWakeNone {
			t.Fatalf("empty projection = %+v, want none", d)
		}
	})

	t.Run("a session whose frozen binding no longer resolves is not spawnable", func(t *testing.T) {
		for _, row := range []collieSchedRow{
			{SessionID: "s1", LastActivityAt: ago(time.Minute)},
			{SessionID: "s1", LastActivityAt: ago(time.Minute), ResumeOwed: true, ResumeMode: "native"},
			{
				SessionID: "s1", LastActivityAt: ago(time.Minute),
				HasLaunch: true, LaunchID: "aaaaaaaaaaaaaaaa", LaunchMode: "native",
				LaunchAbsentConfirmed: true,
			},
		} {
			if d := selectCollieWake([]collieSchedRow{row}, now, idle); d.Kind != collieWakeNone {
				t.Fatalf("decision = %+v for %+v, want none (capability absent)", d, row)
			}
		}
	})

	t.Run("an unresolvable session is skipped, not blocking", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{
			{SessionID: "stranded", LastActivityAt: ago(10 * time.Minute)},
			{SessionID: "live", LastActivityAt: ago(2 * time.Minute), RuntimeReady: true},
		}, now, idle)
		if d.Kind != collieWakeSpawn || d.SessionID != "live" {
			t.Fatalf("decision = %+v, want the younger resolvable session", d)
		}
	})

	t.Run("an unresolvable session still ends on idle", func(t *testing.T) {
		d := selectCollieWake([]collieSchedRow{
			{SessionID: "stranded", LastActivityAt: ago(2 * time.Hour)},
		}, now, idle)
		if d.Kind != collieWakeIdle || d.SessionID != "stranded" {
			t.Fatalf("decision = %+v, want idle-end (idle end needs no runtime)", d)
		}
	})

	t.Run("a spawn decision carries the resolved capability", func(t *testing.T) {
		runtime := PrivateRuntimeBinding{ID: "claude", Harness: "claude-sdk", Channel: "claude"}
		d := selectCollieWake([]collieSchedRow{{
			SessionID: "s1", LastActivityAt: ago(time.Minute),
			Binding: "claude-sdk/claude", Runtime: runtime, RuntimeReady: true,
		}}, now, idle)
		if d.Kind != collieWakeSpawn || !reflect.DeepEqual(d.Runtime, runtime) {
			t.Fatalf("decision = %+v, want the resolved runtime carried", d)
		}
	})
}
