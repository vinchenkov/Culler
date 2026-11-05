//go:build nightly

package property

import (
	"reflect"
	"testing"

	"mc/dispatch"
)

func TestDispatchStateFuzzer(t *testing.T) {
	seeds := []int64{6, 21, 40, 601, 2101, 4001, 20260713, 26071301}
	for _, seed := range seeds {
		t.Run("seed-"+intString(seed), func(t *testing.T) {
			for i := 0; i < 2048; i++ {
				sample := generateDispatchSample(seed, i)
				recordsBefore := cloneRecords(sample.Records)
				lockBefore := cloneLock(sample.Lock)
				configBefore := sample.Config
				clockBefore := sample.Clock

				a := dispatch.Decide(sample.Records, sample.Lock, sample.Config, sample.Clock)
				if err := validateDecide(a); err != nil {
					t.Fatalf("seed=%d case=%d: %v\nrecords=%+v\nlock=%+v",
						seed, i, err, sample.Records, sample.Lock)
				}
				b := dispatch.Decide(sample.Records, sample.Lock, sample.Config, sample.Clock)
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("seed=%d case=%d nondeterministic:\nfirst=%+v\nsecond=%+v",
						seed, i, a, b)
				}
				if !reflect.DeepEqual(sample.Records, recordsBefore) ||
					!reflect.DeepEqual(sample.Lock, lockBefore) ||
					!reflect.DeepEqual(sample.Config, configBefore) ||
					!reflect.DeepEqual(sample.Clock, clockBefore) {
					t.Fatalf("seed=%d case=%d Decide mutated an input", seed, i)
				}
			}
		})
	}
}

func TestWikiDecisionFuzzer(t *testing.T) {
	seeds := []int64{6, 21, 40, 601, 2101, 4001, 20260713, 26071301}
	for _, seed := range seeds {
		t.Run("seed-"+intString(seed), func(t *testing.T) {
			for i := 0; i < 2048; i++ {
				s := generateWikiSample(seed, i)
				if _, err := checkWikiDecision(s); err != nil {
					t.Fatalf("seed=%d case=%d: %v\nrecords=%+v", seed, i, err, s.Records)
				}
			}
		})
	}
}

func TestMetamorphicIneligibleRowsAreInvisible(t *testing.T) {
	for _, seed := range []int64{1801, 1802, 1803, 1804} {
		for i := 0; i < 256; i++ {
			for _, class := range []string{"blocked", "archived", "paused-project"} {
				c := dominantIneligibleCase(seed, i, class)
				base, extended, _ := evaluateInvisibilityCase(c)
				if !sameAction(base, extended) {
					t.Fatalf("seed=%d case=%d class=%s inserted=%d changed winner:\nbase=%+v\nextended=%+v",
						seed, i, class, c.Inserted, base, extended)
				}
			}
			c := archivedPacketCase(seed, i)
			base, extended, _ := evaluateInvisibilityCase(c)
			if !sameAction(base, extended) {
				t.Fatalf("seed=%d case=%d archived packet history changed winner:\nbase=%+v\nextended=%+v",
					seed, i, base, extended)
			}
		}
	}
}

func intString(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
