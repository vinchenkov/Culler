package main

// The Darwin host half of dispatch is resident-only. Each tick receives one
// inherited AF_UNIX stream at descriptor 3; a shell invocation has no
// channel and is refused before helper selection or mutation. The channel
// begins with one closed, canonical hello/hello_ack exchange. Later control
// operations share this transport, so the broker keeps it open until the one
// external dispatch command finishes.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"mc/substrate"
	"mc/verbs"
)

const (
	gatewayControlVersion = verbs.GatewayControlVersion
	configSchemaVersion   = 1
	maxControlFrameBytes  = 64 * 1024
)

// releaseBuildID is replaced by release builds with
// -ldflags '-X main.releaseBuildID=<immutable release id>'. Development
// binaries intentionally match only residents configured for development.
var releaseBuildID = "development"

// releaseVersion is replaced by builds with
// -ldflags '-X main.releaseVersion=<VERSION file>'. Development binaries
// have no version.
var releaseVersion = ""

type controlHello struct {
	Op                  string `json:"op"`
	Seq                 int    `json:"seq"`
	ReleaseBuildID      string `json:"release_build_id"`
	ControlVersion      int    `json:"gateway_control_version"`
	SpineSchemaID       string `json:"spine_schema_id"`
	ConfigSchemaVersion int    `json:"config_schema_version"`
	DeploymentUUID      string `json:"deployment_uuid"`
}

type collieInventoryRequest struct {
	Op  string `json:"op"`
	Seq int    `json:"seq"`
}

type collieInventoryResponse struct {
	Op         string                         `json:"op"`
	Seq        int                            `json:"seq"`
	Containers []verbs.PrivateCollieContainer `json:"containers"`
}

type pipelineLivenessRequest struct {
	Op  string `json:"op"`
	Seq int    `json:"seq"`
}

type pipelineLivenessResponse struct {
	Op       string                          `json:"op"`
	Seq      int                             `json:"seq"`
	Observed []verbs.PrivatePipelineLiveness `json:"observed"`
}

func brokerDispatch(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	conn, err := inheritedResidentControl()
	if err != nil {
		return writeBrokerRefusal(stdout, stderr, "preflight.frame_invalid", err)
	}
	defer conn.Close()

	deploymentUUID, err := verbs.ResolveDispatchDeployment()
	if err != nil {
		return writeBrokerRefusal(stdout, stderr, "preflight.frame_invalid", err)
	}
	if err := exchangeControlHello(conn, deploymentUUID); err != nil {
		return writeBrokerRefusal(stdout, stderr, "preflight.frame_invalid", err)
	}
	inventory, err := requestCollieInventory(conn)
	if err != nil {
		return writeBrokerRefusal(stdout, stderr, "preflight.frame_invalid", err)
	}
	liveness, err := requestPipelineLiveness(conn)
	if err != nil {
		return writeBrokerRefusal(stdout, stderr, "preflight.frame_invalid", err)
	}
	return brokerPrepareCommit(deploymentUUID, inventory, liveness, stdout, stderr)
}

func writeBrokerRefusal(stdout, stderr io.Writer, code string, err error) int {
	msg := err.Error()
	fmt.Fprintln(stderr, "mc:", msg)
	writeErrorEnvelope(stdout, stderr, code, msg)
	return 1
}

func inheritedResidentControl() (net.Conn, error) {
	// Set the inherited descriptor close-on-exec immediately: neither the
	// helper Docker CLI nor any later runtime subprocess may inherit it.
	unix.CloseOnExec(3)
	f := os.NewFile(3, "resident-control")
	if f == nil {
		return nil, fmt.Errorf("mc dispatch requires inherited resident control descriptor 3")
	}
	conn, err := net.FileConn(f)
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("mc dispatch requires inherited resident control descriptor 3: %w", err)
	}
	if _, ok := conn.(*net.UnixConn); !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("mc dispatch resident control descriptor 3 is not an AF_UNIX stream")
	}
	return conn, nil
}

func exchangeControlHello(conn net.Conn, deploymentUUID string) error {
	if !validReleaseBuildID(releaseBuildID) {
		return fmt.Errorf("release build id is not a bounded structural token")
	}
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("set resident control hello deadline: %w", err)
	}
	defer conn.SetDeadline(time.Time{})

	hello := controlHello{
		Op:                  "hello",
		Seq:                 1,
		ReleaseBuildID:      releaseBuildID,
		ControlVersion:      gatewayControlVersion,
		SpineSchemaID:       substrate.CurrentSchemaID,
		ConfigSchemaVersion: configSchemaVersion,
		DeploymentUUID:      deploymentUUID,
	}
	if err := writeControlFrame(conn, hello); err != nil {
		return fmt.Errorf("write resident control hello: %w", err)
	}
	want := hello
	want.Op = "hello_ack"
	var got controlHello
	if err := readCanonicalControlFrame(conn, &got); err != nil {
		return fmt.Errorf("read resident control hello_ack: %w", err)
	}
	if got != want {
		return fmt.Errorf("resident control hello_ack identity mismatch: %s", helloAckMismatchDetail(got, want))
	}
	return nil
}

// helloAckMismatchDetail names the fields that differ, for the same reason the
// resident's own hello refusal does (resident-control.ts helloMismatchDetail):
// the bare refusal names the handshake and not the cause, and the two causes
// that happen in practice — a schema constant raised on one side only, and a
// release identity that moved because `build-prod.sh` ran under a live
// deployment — are each one field.
//
// This is the ACK leg, so a mismatch means the resident echoed something other
// than what `mc` sent; the resident refuses a bad hello before ever acking. That
// makes it the rarer of the two, and the one where naming the field matters most
// because nothing else about it is guessable.
//
// The deployment uuid is reported as differing without its values, exactly as on
// the resident side: which field is wrong is the whole diagnosis, and a
// deployment identifier is not something a diagnostic decides to print.
func helloAckMismatchDetail(got, want controlHello) string {
	var differs []string
	if got.Op != want.Op || got.Seq != want.Seq {
		differs = append(differs, fmt.Sprintf("frame %s/%d (expected %s/%d)", got.Op, got.Seq, want.Op, want.Seq))
	}
	if got.ReleaseBuildID != want.ReleaseBuildID {
		differs = append(differs, fmt.Sprintf("release_build_id %s (expected %s)", got.ReleaseBuildID, want.ReleaseBuildID))
	}
	if got.ControlVersion != want.ControlVersion {
		differs = append(differs, fmt.Sprintf("gateway_control_version %d (expected %d)", got.ControlVersion, want.ControlVersion))
	}
	if got.SpineSchemaID != want.SpineSchemaID {
		differs = append(differs, fmt.Sprintf("spine_schema_id %s (expected %s)", got.SpineSchemaID, want.SpineSchemaID))
	}
	if got.ConfigSchemaVersion != want.ConfigSchemaVersion {
		differs = append(differs, fmt.Sprintf("config_schema_version %d (expected %d)", got.ConfigSchemaVersion, want.ConfigSchemaVersion))
	}
	if got.DeploymentUUID != want.DeploymentUUID {
		differs = append(differs, "deployment_uuid differs")
	}
	if len(differs) == 0 {
		// The struct compared unequal while every field compares equal, which is
		// a comparison bug rather than an identity mismatch. Say that instead of
		// an empty detail.
		return "the frames agree field by field; the struct comparison disagrees"
	}
	return strings.Join(differs, "; ")
}

func requestCollieInventory(conn net.Conn) ([]verbs.PrivateCollieContainer, error) {
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, fmt.Errorf("set resident control inventory deadline: %w", err)
	}
	defer conn.SetDeadline(time.Time{})
	if err := writeControlFrame(conn, collieInventoryRequest{Op: "collie_inventory", Seq: 2}); err != nil {
		return nil, fmt.Errorf("write resident control Collie inventory request: %w", err)
	}
	var response collieInventoryResponse
	if err := readCanonicalControlFrame(conn, &response); err != nil {
		return nil, fmt.Errorf("read resident control Collie inventory response: %w", err)
	}
	if response.Op != "collie_inventory_result" || response.Seq != 2 {
		return nil, fmt.Errorf("resident control Collie inventory response identity mismatch")
	}
	if err := verbs.ValidatePrivateCollieInventory(response.Containers); err != nil {
		return nil, err
	}
	return response.Containers, nil
}

// requestPipelineLiveness reads the resident's host observation of recently
// launched pipeline runs (seq 3, after the Collie inventory). The resident
// gathers it BEFORE it spawns this process, precisely because this exchange
// carries a 2s deadline on both sides that serial `docker inspect` calls would
// blow — and a blown control exchange refuses the whole tick.
//
// Validated here, at the broker, as well as in the request constructor and at
// the top of DispatchPreparePrivate: the helper never trusts the broker's
// shape, and the broker never trusts the resident's.
func requestPipelineLiveness(conn net.Conn) ([]verbs.PrivatePipelineLiveness, error) {
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, fmt.Errorf("set resident control pipeline liveness deadline: %w", err)
	}
	defer conn.SetDeadline(time.Time{})
	if err := writeControlFrame(conn, pipelineLivenessRequest{Op: "pipeline_liveness", Seq: 3}); err != nil {
		return nil, fmt.Errorf("write resident control pipeline liveness request: %w", err)
	}
	var response pipelineLivenessResponse
	if err := readCanonicalControlFrame(conn, &response); err != nil {
		return nil, fmt.Errorf("read resident control pipeline liveness response: %w", err)
	}
	if response.Op != "pipeline_liveness_result" || response.Seq != 3 {
		return nil, fmt.Errorf("resident control pipeline liveness response identity mismatch")
	}
	if err := verbs.ValidatePrivatePipelineLiveness(response.Observed); err != nil {
		return nil, err
	}
	return response.Observed, nil
}

func validReleaseBuildID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			(i > 0 && (r == '.' || r == '_' || r == '+' || r == '-')) {
			continue
		}
		return false
	}
	return true
}

func writeControlFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > maxControlFrameBytes {
		return fmt.Errorf("control frame length %d exceeds the 64-KiB bound", len(body))
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	return writeAll(w, append(prefix[:], body...))
}

func readCanonicalControlFrame(r io.Reader, dst any) error {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > maxControlFrameBytes {
		return fmt.Errorf("control frame length %d is outside 1..65536", n)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return err
	}
	canonical, err := json.Marshal(dst)
	if err != nil {
		return err
	}
	// Exact re-encoding rejects unknown or duplicate fields, reordered keys,
	// alternate number spellings, whitespace, and trailing bytes at once.
	if !bytes.Equal(body, canonical) {
		return fmt.Errorf("control frame is not the closed canonical encoding")
	}
	return nil
}
