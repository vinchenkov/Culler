//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSetupReadinessWaitsForTheFirstAgentsViewAnswer(t *testing.T) {
	clock := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	pings := 0
	awaitAgentsView("127.0.0.1:7334", func(context.Context, string) error {
		pings++
		if pings < 3 {
			return errors.New("AgentsView did not answer")
		}
		return nil
	}, func(d time.Duration) { clock = clock.Add(d) }, func() time.Time { return clock }, time.Minute)
	if pings != 3 {
		t.Fatalf("pings = %d, want 3", pings)
	}
}

func TestSetupReadinessStopsTheAgentsViewWaitAtItsLimit(t *testing.T) {
	clock := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	start := clock
	awaitAgentsView("127.0.0.1:7334", func(context.Context, string) error {
		return errors.New("AgentsView did not answer")
	}, func(d time.Duration) { clock = clock.Add(d) }, func() time.Time { return clock }, 10*time.Second)
	if waited := clock.Sub(start); waited != 10*time.Second {
		t.Fatalf("waited %s, want 10s", waited)
	}
}
