package onboarding

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"
)

func TestActivityProgressReportsBeforeCompletion(t *testing.T) {
	out := newProgressCapture()
	terminal := NewTerminal(nil, bufio.NewWriter(out))
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	done := make(chan error, 1)
	go func() {
		done <- terminal.ActivityProgress("Prepare the container", func(report func(string)) error {
			report("Download the container base")
			<-release
			report("Save the container image")
			return nil
		})
	}()
	out.waitFor(t, "Update: Download the container base")
	if strings.Contains(out.String(), "Complete:") {
		t.Fatal("the display declared completion before the action returned")
	}
	select {
	case err := <-done:
		t.Fatalf("the action returned before release: %v", err)
	default:
	}
	finish()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the action did not return")
	}
	if output := out.String(); !strings.Contains(output, "Update: Save the container image") || !strings.Contains(output, "Complete: Prepare the container") {
		t.Fatalf("the display lost the final update or result: %q", output)
	}
}

func TestActivityProgressShowsOnlyActiveStep(t *testing.T) {
	out := newProgressCapture()
	terminal := NewTerminal(nil, out)
	clock := newProgressClock()
	want := "Active: Prepare the container\n"
	assertOutput := func() {
		t.Helper()
		if output := out.String(); output != want {
			t.Fatalf("the display = %q; want %q", output, want)
		}
	}
	assertSilentTicks := func(duration time.Duration) {
		t.Helper()
		clock.advance(duration)
		clock.tick(t)
		// The second tick confirms that the worker processed the first tick.
		clock.tick(t)
		assertOutput()
	}
	err := terminal.activityProgress("Prepare the container", func(report func(string)) error {
		assertOutput()
		assertSilentTicks(10 * time.Second)
		report("Download the container base")
		want += "Update: Download the container base\n"
		assertOutput()
		assertSilentTicks(12 * time.Second)
		report("Download the container base")
		assertOutput()
		assertSilentTicks(10 * time.Second)
		report("Build the agent tools")
		want += "Update: Build the agent tools\n"
		assertOutput()
		report("Save the container image")
		assertOutput()
		clock.advance(time.Second)
		clock.tick(t)
		out.waitFor(t, "Update: Save the container image")
		want += "Update: Save the container image\n"
		assertOutput()
		assertSilentTicks(69 * time.Second)
		return nil
	}, clock.now, clock.ticks)
	if err != nil {
		t.Fatal(err)
	}
	want += "✓ Complete: Prepare the container\n"
	assertOutput()
}

func TestActivityProgressCombinesRapidReports(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(nil, &out)
	clock := newProgressClock()
	err := terminal.activityProgress("Prepare the container", func(report func(string)) error {
		report("Read the container definition")
		for step := range 1000 {
			report(fmt.Sprintf("Build step %d", step))
		}
		if count := strings.Count(out.String(), "Update:"); count != 1 {
			t.Fatalf("a report burst produced %d history lines", count)
		}
		return nil
	}, clock.now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(out.String(), "Update:"); count != 2 || !strings.Contains(out.String(), "Build step 999") {
		t.Fatalf("the display lost the latest report: %q", out.String())
	}
}

func TestActivityProgressRejectsControlTextAndFitsWidth(t *testing.T) {
	for _, columns := range []int{80, 40, 20, 7, 1} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			t.Setenv("TERM", "dumb")
			var out bytes.Buffer
			terminal := NewTerminal(nil, &out)
			terminal.columns = columns
			clock := newProgressClock()
			err := terminal.activityProgress("Prepare\x1b[31m the container\x1b[0m", func(report func(string)) error {
				report("Read /Users/operator/projects/项目/a-directory-with-a-long-name/Containerfile\r\n\t now\x00")
				clock.advance(time.Second)
				report("Download\x1b]0;hidden-title\a the base\u009b31m\u009b0m\u202e")
				clock.advance(time.Second)
				report(strings.Repeat("x", 10000))
				return nil
			}, clock.now, nil)
			if err != nil {
				t.Fatal(err)
			}
			output := out.String()
			for _, line := range strings.Split(output, "\n") {
				if displayWidth(line) > columns {
					t.Fatalf("width %d exceeded by %q", columns, line)
				}
			}
			for _, r := range output {
				if (unicode.IsControl(r) && r != '\n') || unicode.Is(unicode.Cf, r) {
					t.Fatalf("the display retained a terminal control: %q", r)
				}
			}
			joined := strings.ReplaceAll(output, "\n", "")
			if strings.Contains(joined, "hidden-title") || strings.Contains(joined, "31m") {
				t.Fatalf("the display retained a control payload: %q", output)
			}
			if strings.Count(joined, "x") != 512 || !strings.Contains(joined, "...") {
				t.Fatal("the display failed to limit a long report")
			}
			if columns > 1 && !strings.Contains(joined, "/Users/operator/projects/项目/a-directory-with-a-long-name/Containerfile") {
				t.Fatal("the display changed the report path")
			}
		})
	}
}

func TestActivityProgressPreservesOutcomesAndStopsReports(t *testing.T) {
	failure := errors.New("private diagnostic detail")
	for _, tc := range []struct {
		name, status string
		err          error
		panicValue   any
	}{
		{name: "complete", status: "Complete:"},
		{name: "failure", status: "Failed:", err: failure},
		{name: "back", status: "Stopped:", err: ErrBack},
		{name: "exit", status: "Stopped:", err: ErrExit},
		{name: "panic", status: "Stopped:", panicValue: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newProgressCapture()
			terminal := NewTerminal(nil, out)
			clock := newProgressClock()
			var lateReport func(string)
			var result error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				result = terminal.activityProgress("Prepare the container", func(report func(string)) error {
					lateReport = report
					report("Read the container definition")
					report("Download the container base")
					clock.advance(time.Second)
					clock.tick(t)
					out.waitFor(t, "Update: Download the container base")
					if tc.panicValue != nil {
						panic(tc.panicValue)
					}
					return tc.err
				}, clock.now, clock.ticks)
			}()
			if !errors.Is(result, tc.err) || recovered != tc.panicValue {
				t.Fatalf("result = %v, panic = %v; want %v, %v", result, recovered, tc.err, tc.panicValue)
			}
			output := out.String()
			if !strings.Contains(output, tc.status+" Prepare the container") || strings.Contains(output, failure.Error()) {
				t.Fatalf("the display changed the outcome or exposed details: %q", output)
			}
			lateReport("This report follows the result")
			if out.String() != output {
				t.Fatal("a report wrote after the action ended")
			}
			select {
			case clock.ticks <- time.Now():
				t.Fatal("the display worker received a tick after return")
			default:
			}
		})
	}
	terminal := NewTerminal(nil, nil)
	if err := terminal.ActivityProgress("Prepare the container", nil); err == nil {
		t.Fatal("an absent action returned success")
	}
}

func TestActivityProgressSerializesReportsAndPanels(t *testing.T) {
	out := &progressConcurrentWriter{}
	terminal := NewTerminal(nil, out)
	clock := newProgressClock()
	err := terminal.activityProgress("Prepare the container", func(report func(string)) error {
		var workers sync.WaitGroup
		for range 16 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for range 20 {
					report("Download the container base")
					terminal.Panel("Container status", []string{"The container operation is active."})
				}
			}()
		}
		workers.Wait()
		return nil
	}, clock.now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.overlap.Load() {
		t.Fatal("terminal writes overlapped")
	}
}

func TestActivityProgressPreservesBrowserAddress(t *testing.T) {
	for _, columns := range []int{80, 40} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			out := newProgressCapture()
			terminal := NewTerminal(nil, out)
			terminal.ansi, terminal.columns = true, columns
			clock := newProgressClock()
			address := "https://example.test/authorize?client_id=fixture&state=fixture"
			err := terminal.activityProgress("Connect this account", func(func(string)) error {
				terminal.Panel("Sign in through your browser", []string{"Use the address below."})
				clock.advance(time.Second)
				clock.tick(t)
				out.waitFor(t, "Connect this account ..")
				if _, err := fmt.Fprintf(terminal, "%s\n\n", address); err != nil {
					return err
				}
				offset := len(out.String())
				clock.advance(time.Second)
				clock.tick(t)
				out.waitFor(t, "Connect this account ...")
				if strings.Contains(out.String()[offset:], "\x1b[J") {
					t.Fatal("The progress update erased the browser address.")
				}
				if !strings.Contains(out.String(), address+"\n\n") {
					t.Fatal("The terminal changed the browser address.")
				}
				return nil
			}, clock.now, clock.ticks)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

type progressClock struct {
	elapsed atomic.Int64
	ticks   chan time.Time
}

func newProgressClock() *progressClock { return &progressClock{ticks: make(chan time.Time)} }

func (clock *progressClock) now() time.Time {
	return time.Unix(1700000000, clock.elapsed.Load())
}

func (clock *progressClock) advance(duration time.Duration) { clock.elapsed.Add(int64(duration)) }

func (clock *progressClock) tick(t *testing.T) {
	t.Helper()
	select {
	case clock.ticks <- clock.now():
	case <-time.After(3 * time.Second):
		t.Fatal("the display worker did not receive a tick")
	}
}

type progressCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	wrote  chan struct{}
}

func newProgressCapture() *progressCapture { return &progressCapture{wrote: make(chan struct{}, 1)} }

func (out *progressCapture) Write(data []byte) (int, error) {
	out.mu.Lock()
	n, err := out.buffer.Write(data)
	out.mu.Unlock()
	select {
	case out.wrote <- struct{}{}:
	default:
	}
	return n, err
}

func (out *progressCapture) String() string {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.buffer.String()
}

func (out *progressCapture) waitFor(t *testing.T, text string) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for !strings.Contains(out.String(), text) {
		select {
		case <-out.wrote:
		case <-timer.C:
			t.Fatalf("the display lacks %q: %q", text, out.String())
		}
	}
}

type progressConcurrentWriter struct {
	active  atomic.Int32
	overlap atomic.Bool
}

func (out *progressConcurrentWriter) Write(data []byte) (int, error) {
	if out.active.Add(1) != 1 {
		out.overlap.Store(true)
	}
	runtime.Gosched()
	out.active.Add(-1)
	return len(data), nil
}

func TestActivityProgressAnimatesWithoutCommandOutput(t *testing.T) {
	out := newProgressCapture()
	terminal := NewTerminal(nil, out)
	terminal.ansi = true
	clock := newProgressClock()
	err := terminal.activityProgress("Prepare the container", func(report func(string)) error {
		clock.advance(time.Second)
		clock.tick(t)
		out.waitFor(t, "Prepare the container ..")
		report("Install the agent tools")
		clock.advance(time.Second)
		clock.tick(t)
		out.waitFor(t, "Install the agent tools ...")
		clock.advance(time.Second)
		clock.tick(t)
		out.waitFor(t, "Install the agent tools .")
		if strings.Contains(out.String(), "✓") {
			t.Fatal("The active operation reported success.")
		}
		return nil
	}, clock.now, clock.ticks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "✓ Prepare the container") {
		t.Fatal("The operation omitted its final result.")
	}
}
