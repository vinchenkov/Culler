package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"mc/verbs"
)

type closedPrivateTestFrame struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
}

func TestPrivateHelperSelfDeadlineTerminatesContainerSideProcess(t *testing.T) {
	if os.Getenv("MC_TEST_PRIVATE_SELF_DEADLINE") == "1" {
		synctest.Test(t, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			const budget = 20 * time.Millisecond
			deadline := time.Now().Add(budget)
			go func() {
				code := runPrivateDispatchInScope("__dispatch-prepare", deadline, reader, os.Stdout, os.Stderr)
				fmt.Fprintf(os.Stderr, "The helper returned %d. It must terminate its process.\n", code)
				os.Exit(99)
			}()
			synctest.Wait()
			time.Sleep(budget - time.Nanosecond)
			fmt.Fprintln(os.Stdout, "The helper remains alive before its deadline.")
			time.Sleep(time.Nanosecond)
			synctest.Wait()
			t.Fatal("the helper remains alive at its deadline")
		})
		return
	}

	output := privateHelperDeadlineProcess(t, "MC_TEST_PRIVATE_SELF_DEADLINE")
	if !strings.Contains(output, "The helper remains alive before its deadline.") {
		t.Fatalf("the helper terminated before its deadline: %s", output)
	}
}

func TestPrivateHelperAbsoluteDeadlineIncludesBrokerStartup(t *testing.T) {
	if os.Getenv("MC_TEST_PRIVATE_ABSOLUTE_DEADLINE") == "1" {
		synctest.Test(t, func(t *testing.T) {
			deadline := time.Now().Add(20 * time.Millisecond)
			time.Sleep(40 * time.Millisecond) // The broker consumes the budget before the helper starts.
			code := runPrivateDispatchInScope("__dispatch-commit", deadline, privateExpiredDeadlineReader{t}, os.Stdout, os.Stderr)
			t.Fatalf("The expired helper returned %d. It must terminate its process.", code)
		})
		return
	}

	privateHelperDeadlineProcess(t, "MC_TEST_PRIVATE_ABSOLUTE_DEADLINE")
}

type privateExpiredDeadlineReader struct{ t *testing.T }

func (r privateExpiredDeadlineReader) Read([]byte) (int, error) {
	r.t.Fatal("the expired helper read its request")
	return 0, io.EOF
}

func privateHelperDeadlineProcess(t *testing.T, childEnv string) string {
	t.Helper()
	// This timeout contains a stalled test. Virtual time checks the production deadline in the child.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != privateHelperTimeoutExitCode {
		t.Fatalf("The helper exit was %v. Its exit code must be %d.\n%s", err, privateHelperTimeoutExitCode, output)
	}
	return string(output)
}

func TestPrivateHelperNoProgressDeadline(t *testing.T) {
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "docker")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexec sleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldProgress, oldWall := helperNoProgressTimeout, helperWallTimeout
	helperNoProgressTimeout, helperWallTimeout = 20*time.Millisecond, time.Second
	t.Cleanup(func() { helperNoProgressTimeout, helperWallTimeout = oldProgress, oldWall })

	start := time.Now()
	err := callPrivateHelper("__dispatch-prepare", closedPrivateTestFrame{Version: 1, Name: "request"}, &closedPrivateTestFrame{})
	if err == nil || !strings.Contains(err.Error(), "no I/O progress") {
		t.Fatalf("deadline error = %v, want no-progress refusal", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("no-progress refusal took %s, want bounded prompt kill", elapsed)
	}
}

func TestPrivateFrameClosedCanonicalAndBounded(t *testing.T) {
	good := closedPrivateTestFrame{Version: 1, Name: "candidate"}
	var encoded bytes.Buffer
	if err := writePrivateFrame(&encoded, good); err != nil {
		t.Fatal(err)
	}
	var decoded closedPrivateTestFrame
	if err := readPrivateFrame(bytes.NewReader(encoded.Bytes()), &decoded); err != nil {
		t.Fatalf("canonical frame rejected: %v", err)
	}
	if decoded != good {
		t.Fatalf("decoded = %+v, want %+v", decoded, good)
	}

	for name, body := range map[string][]byte{
		"unknown_field":   []byte(`{"version":1,"name":"candidate","extra":true}`),
		"duplicate_field": []byte(`{"version":1,"name":"candidate","name":"candidate"}`),
		"reordered":       []byte(`{"name":"candidate","version":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			var frame bytes.Buffer
			var prefix [4]byte
			binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
			frame.Write(prefix[:])
			frame.Write(body)
			if err := readPrivateFrame(&frame, &closedPrivateTestFrame{}); err == nil {
				t.Fatal("non-canonical private frame was accepted")
			}
		})
	}

	var oversized [4]byte
	binary.BigEndian.PutUint32(oversized[:], maxPrivateFrameBytes+1)
	if err := readPrivateFrame(bytes.NewReader(oversized[:]), &closedPrivateTestFrame{}); err == nil {
		t.Fatal("oversized private frame was accepted")
	}
}

// A broker failure must carry the helper's own reason. Without the child's
// stderr every private-helper failure reads as the bare "private helper <verb>
// failed", which leaves an intermittent __dispatch-prepare failure
// undiagnosable. The exit error is carried too: exit 124 is the container-side
// absolute deadline, the signature of a docker exec that spent the broker
// allowance on its own start.
func TestHelperReasonCarriesTheChildsEvidence(t *testing.T) {
	var buf boundedBuffer
	buf.limit = maxHelperStderrBytes
	buf.Write([]byte("mc: private prepare refused\n"))

	got := helperReason(&buf, errors.New("exit status 1"))
	for _, want := range []string{"exit status 1", "private prepare refused"} {
		if !strings.Contains(got, want) {
			t.Fatalf("helperReason = %q, want it to carry %q", got, want)
		}
	}

	// Nothing to report stays empty, so the success path is unchanged.
	var empty boundedBuffer
	empty.limit = maxHelperStderrBytes
	if got := helperReason(&empty, nil); got != "" {
		t.Fatalf("helperReason with no evidence = %q, want empty", got)
	}

	// The tail is bounded and says so, so a chatty helper cannot use the
	// diagnostic as a data channel.
	var big boundedBuffer
	big.limit = 64
	big.Write([]byte(strings.Repeat("x", 500)))
	got = helperReason(&big, nil)
	if !strings.Contains(got, "truncated") {
		t.Fatalf("helperReason over its bound = %q, want it marked truncated", got)
	}
	if len(got) > 200 {
		t.Fatalf("helperReason over its bound is %d bytes, want it held near its limit", len(got))
	}

	// Newlines are folded so the reason stays one log line.
	var multi boundedBuffer
	multi.limit = maxHelperStderrBytes
	multi.Write([]byte("first\nsecond\n"))
	if got := helperReason(&multi, nil); strings.Contains(got, "\n") {
		t.Fatalf("helperReason = %q, want no embedded newline", got)
	}
}

// TestCapabilityConsumingFinalIsTheBrokersConverseFence pins the second half of
// the runtime-capability drift fence. The helper naming the catalog it consumed
// is a PRODUCER fence; on its own it is fail-open, because a frame that simply
// omits the name would look like an effect owing no check. The broker therefore
// reads the final's own action discriminator, and an unreadable body owes the
// fence rather than escaping it.
func TestCapabilityConsumingFinalIsTheBrokersConverseFence(t *testing.T) {
	for name, tc := range map[string]struct {
		final string
		want  bool
	}{
		"a homie wake consumes the catalog":     {`{"action":"homie-wake","session":"h-1"}`, true},
		"a carried runtime binding consumes it": {`{"action":"future-launch","runtime_binding":{"id":"claude"}}`, true},
		"an idle end does not":                  {`{"action":"homie-stop","session":"h-1"}`, false},
		"a pipeline spawn effect does not":      {`{"action":"spawn","run_id":"r-1"}`, false},
		"an unreadable body owes the fence":     {`{"action":`, true},
		"a non-object body owes the fence":      {`"homie-wake"`, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := capabilityConsumingFinal([]byte(tc.final)); got != tc.want {
				t.Fatalf("capabilityConsumingFinal(%s) = %v, want %v", tc.final, got, tc.want)
			}
		})
	}
}

// A helper refusal already carries its classification in its own exit code:
// 1 is a domain rejection, 2 is usage or environment. The brokered lane used to
// flatten every failure into Usagef, so on the ONLY path production takes, a
// plain domain rejection — "that channel is already bound to an active session"
// — surfaced as exit 2 with envelope code "usage".
func TestPrivateHelperVerbErrorKeepsTheHelpersContractCode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
	}{
		{"helper domain rejection", &privateHelperExitError{
			message: "private helper __homie-start failed: already bound", code: 1,
		}, "domain-rejection"},
		{"helper usage refusal", &privateHelperExitError{
			message: "private helper __homie-start failed: bad frame", code: 2,
		}, "usage"},
		// A crossing that never produced an exit code at all (timeout, docker
		// missing) is an environment failure, not a domain verdict.
		{"crossing failure", errors.New("exceeded wall allowance"), "usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := writeVerbError(&stdout, &stderr,
				privateHelperVerbError("private Homie start", tc.err))
			wantExit := map[string]int{"domain-rejection": 1, "usage": 2}[tc.wantCode]
			if code != wantExit {
				t.Fatalf("exit = %d, want %d", code, wantExit)
			}
			if !strings.Contains(stdout.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("envelope = %s, want code %q", stdout.String(), tc.wantCode)
			}
		})
	}
}

// TestBrokerTickRecordsTheRoundThatEndedIt pins the one tick object per
// invocation for the tick's early end: a round that the crossing refused, or
// whose committed effect the broker cannot read, after a completed round is
// recorded as the tick's last action — the same error envelope the in-process
// seam appends — so the written tick states that it ended early and why.
// Before any round completed the same failure is the single broker refusal,
// exactly as a plan-frame failure.
func TestBrokerTickRecordsTheRoundThatEndedIt(t *testing.T) {
	for name, tc := range map[string]struct {
		effect  string
		refusal *brokerTickRefusal
		code    string
	}{
		"a refused crossing": {
			refusal: &brokerTickRefusal{code: "health.helper_unavailable", err: errors.New("helper down")},
			code:    "health.helper_unavailable",
		},
		"an unreadable round result": {effect: `{"action":`, code: "preflight.frame_invalid"},
		"an empty round result":      {effect: ``, code: "preflight.frame_invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			var tick brokerTick
			ended, first, err := tick.fold("a", json.RawMessage(tc.effect), tc.refusal)
			if err != nil || !ended || first == nil || first.code != tc.code {
				t.Fatalf("first round: ended=%v first=%+v err=%v, want the single broker refusal %s", ended, first, err, tc.code)
			}

			tick = brokerTick{}
			if ended, first, err := tick.fold("", json.RawMessage(`{"action":"homie-wake","session":"h-1"}`), nil); ended || first != nil || err != nil {
				t.Fatalf("homie round: ended=%v first=%+v err=%v", ended, first, err)
			}
			ended, first, err = tick.fold("a", json.RawMessage(tc.effect), tc.refusal)
			if err != nil || !ended || first != nil {
				t.Fatalf("second round: ended=%v first=%+v err=%v, want the tick ended with no broker refusal", ended, first, err)
			}
			encoded, err := tick.marshal()
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Action  string            `json:"action"`
				Actions []json.RawMessage `json:"actions"`
				Idle    []json.RawMessage `json:"idle"`
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if got.Action != "tick" || len(got.Actions) != 2 || len(got.Idle) != 0 {
				t.Fatalf("tick = %s, want the Homie effect then the refusal envelope", encoded)
			}
			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(got.Actions[1], &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != tc.code || envelope.Error.Message == "" {
				t.Fatalf("last action = %s, want {\"error\":{\"code\":%q,...}}", got.Actions[1], tc.code)
			}
		})
	}
}

// TestBrokerTickListsPipelineIdleRoundsOnly pins the tick's idle list: a
// pipeline round's idle result is listed with its Project and reason, the
// Homie stage's idle result is recorded nowhere, and every other effect is
// an element of actions in round order.
func TestBrokerTickListsPipelineIdleRoundsOnly(t *testing.T) {
	var tick brokerTick
	for _, round := range []struct{ project, effect string }{
		{"", `{"action":"idle","reason":"nothing-to-do"}`},
		{"a", `{"action":"idle","reason":"lease-held"}`},
		{"b", `{"action":"spawn","project":"b","run_id":"r-1"}`},
	} {
		if ended, first, err := tick.fold(round.project, json.RawMessage(round.effect), nil); ended || first != nil || err != nil {
			t.Fatalf("fold(%s): ended=%v first=%+v err=%v", round.project, ended, first, err)
		}
	}
	encoded, err := tick.marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"action":"tick","actions":[{"action":"spawn","project":"b","run_id":"r-1"}],"idle":[{"project":"a","reason":"lease-held"}]}`
	if string(encoded) != want {
		t.Fatalf("tick = %s\nwant   %s", encoded, want)
	}
}

// A wiki round changes neither the count of completed rounds nor the refusal
// that ended the loop of the pipeline rounds. Its refusal is one more error
// envelope, its idle result is recorded nowhere, and each other effect is an
// action after the effects of the pipeline rounds.
func TestBrokerTickFoldsAWikiRound(t *testing.T) {
	var tick brokerTick
	if ended, first, err := tick.fold("", json.RawMessage(`{"action":"idle","reason":"nothing-to-do"}`), nil); ended || first != nil || err != nil {
		t.Fatalf("homie round: ended=%v first=%+v err=%v", ended, first, err)
	}
	pipeline := &brokerTickRefusal{code: "health.helper_unavailable", err: errors.New("pipeline round refused")}
	if ended, first, err := tick.fold("a", nil, pipeline); !ended || first != nil || err != nil {
		t.Fatalf("pipeline round: ended=%v first=%+v err=%v", ended, first, err)
	}

	for _, effect := range []string{
		`{"action":"idle","project":"a","reason":"wiki-current"}`,
		`{"action":"spawn","project":"b","run_id":"r-2"}`,
	} {
		if err := tick.foldWiki(json.RawMessage(effect), nil); err != nil {
			t.Fatalf("foldWiki(%s): %v", effect, err)
		}
	}
	if err := tick.foldWiki(nil, &brokerTickRefusal{code: "preflight.stale", err: errors.New("wiki round refused")}); err != nil {
		t.Fatal(err)
	}
	if err := tick.foldWiki(json.RawMessage(`{"action":`), nil); err == nil {
		t.Fatal("an unreadable wiki round result was accepted")
	}
	if tick.completed != 1 || tick.refusal != pipeline {
		t.Fatalf("completed=%d refusal=%+v, want the values of the pipeline rounds", tick.completed, tick.refusal)
	}
	encoded, err := tick.marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"action":"tick","actions":[` +
		`{"error":{"code":"health.helper_unavailable","message":"pipeline round refused"}},` +
		`{"action":"spawn","project":"b","run_id":"r-2"},` +
		`{"error":{"code":"preflight.stale","message":"wiki round refused"}}],"idle":[]}`
	if string(encoded) != want {
		t.Fatalf("tick = %s\nwant   %s", encoded, want)
	}
}

// A helper call that reached a time limit ends the wiki loop, because each
// later round can reach the same limit. Each other refusal ends only its
// round. The stub refuses the round of Project a, reaches a time limit in the
// round of Project b, and records each call.
func TestHelperTimeLimitEndsTheWikiLoop(t *testing.T) {
	for name, tc := range map[string]struct {
		limit          string
		progress, wall time.Duration
	}{
		"the time limit of the helper": {limit: "exit 124", progress: helperNoProgressTimeout, wall: helperWallTimeout},
		"the limit for no progress":    {limit: "exec sleep 30", progress: time.Second, wall: time.Minute},
		"the wall limit":               {limit: "exec sleep 30", progress: time.Minute, wall: time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			stubDir := t.TempDir()
			calls := filepath.Join(stubDir, "calls")
			script := "#!/bin/sh\n" +
				"frame=\"" + stubDir + "/frame.$$\"\n" +
				"cat > \"$frame\"\n" +
				"grep -a -o '\"project\":\"[a-z]*\"' \"$frame\" >> \"" + calls + "\"\n" +
				"if grep -a -q '\"project\":\"b\"' \"$frame\"; then " + tc.limit + "; fi\n" +
				"echo 'mc: private prepare refused: the stub refuses each round' >&2\n" +
				"exit 1\n"
			if err := os.WriteFile(filepath.Join(stubDir, "docker"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			// The first start of a new file can be slow. This start has no time
			// limit and an empty input, so the stub records no call.
			_ = exec.Command(filepath.Join(stubDir, "docker")).Run()
			t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			oldProgress, oldWall := helperNoProgressTimeout, helperWallTimeout
			helperNoProgressTimeout, helperWallTimeout = tc.progress, tc.wall
			t.Cleanup(func() { helperNoProgressTimeout, helperWallTimeout = oldProgress, oldWall })

			home := t.TempDir()
			var tick brokerTick
			var stderr bytes.Buffer
			brokerWikiRounds(&tick, home, "de41cafe-0000-4000-8000-000000000001", []string{"a", "b", "c"},
				map[string]string{}, verbs.ResolveRuntimeCapability(home),
				[]verbs.PrivateHomieContainer{}, []verbs.PrivatePipelineLiveness{}, &stderr)

			recorded, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("the stub recorded no call: %v (stderr %q)", err, stderr.String())
			}
			if got, want := string(recorded), "\"project\":\"a\"\n\"project\":\"b\"\n"; got != want {
				t.Fatalf("helper calls = %q, want one call for Project a and one for Project b", got)
			}
			if len(tick.actions) != 2 || tick.completed != 0 || tick.refusal != nil {
				t.Fatalf("tick = %+v, want one envelope for each of the two rounds", tick)
			}
			for i, raw := range tick.actions {
				var envelope struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(raw, &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error.Code != "health.helper_unavailable" || !strings.Contains(envelope.Error.Message, "__dispatch-prepare") {
					t.Fatalf("action %d = %s, want the refusal envelope of the prepare call", i, raw)
				}
			}
		})
	}
}

// Each end of a helper call through a time limit carries the same mark. A
// refusal of the helper carries none.
func TestPrivateHelperTimeLimitMark(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"the time limit of the helper": {&privateHelperExitError{message: "failed", code: privateHelperTimeoutExitCode}, true},
		"the limit of the broker":      {&privateHelperTimeLimitError{message: "exceeded wall allowance"}, true},
		"a domain rejection":           {&privateHelperExitError{message: "failed", code: 1}, false},
		"a usage refusal":              {&privateHelperExitError{message: "failed", code: 2}, false},
		"a start failure":              {errors.New("private helper failed to start"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := errors.Is(tc.err, errPrivateHelperTimeLimit); got != tc.want {
				t.Fatalf("time limit mark of %v = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// The dashboard acts on the code of a refused packet preparation, so the
// broker relays a domain rejection of the helper with its own code and
// message. Each other helper failure keeps the text of the crossing.
func TestHomieStartBrokerRelaysTheHelperRefusal(t *testing.T) {
	const envelope = `{"error":{"code":"record-conflict","message":"The review packet changed. Open the current packet."}}`
	for name, tc := range map[string]struct {
		stdout   string
		exit     int
		wantExit int
		want     string
	}{
		"a domain rejection":      {envelope, 1, 1, envelope},
		"an uncoded rejection":    {`{"error":{"code":"domain-rejection","message":"The selected agent profile is unavailable."}}`, 1, 1, `{"error":{"code":"domain-rejection","message":"The selected agent profile is unavailable."}}`},
		"a usage refusal":         {`{"error":{"code":"usage","message":"invalid private Homie start frame"}}`, 2, 2, `"message":"private Homie start: private helper __homie-start failed`},
		"two envelopes":           {envelope + "\n" + envelope, 1, 1, `"message":"private Homie start: private helper __homie-start failed`},
		"an envelope with a key":  {`{"error":{"code":"record-conflict","message":"changed","detail":"x"}}`, 1, 1, `"message":"private Homie start: private helper __homie-start failed`},
		"an envelope of no error": {`{"packets":[]}`, 1, 1, `"message":"private Homie start: private helper __homie-start failed`},
	} {
		t.Run(name, func(t *testing.T) {
			stubDir := t.TempDir()
			script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\nprintf '%%s\\n' '%s'\necho 'mc: refused' >&2\nexit %d\n", tc.stdout, tc.exit)
			if err := os.WriteFile(filepath.Join(stubDir, "docker"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			var result verbs.HomieStartResult
			err := callPrivateHelper("__homie-start", verbs.PrivateHomieStartRequest{Version: 1, From: "dashboard:dash-0a1b2c3d", Packet: 41, PacketVersion: 88}, &result)
			var stdout, stderr bytes.Buffer
			if code := writeVerbError(&stdout, &stderr, privateHelperRefusal("private Homie start", err)); code != tc.wantExit {
				t.Fatalf("exit = %d, want %d (%s)", code, tc.wantExit, stdout.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("envelope = %s, want %s", stdout.String(), tc.want)
			}
			if wrapped := privateHelperVerbError("private dispatch prepare", err); !strings.Contains(wrapped.Error(), "private helper __homie-start failed") {
				t.Fatalf("the crossing text changed: %v", wrapped)
			}
		})
	}
}
