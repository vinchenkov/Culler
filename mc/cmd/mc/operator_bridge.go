package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"mc/deployment"
	"mc/operatorcmd"
	"mc/verbs"
)

var activeOperatorRequest string
var operatorOwnerDepth int
var operatorReadDepth int

// enterOperatorOwner retains the installer scope across its nested commands.
func enterOperatorOwner(args []string) func() {
	if len(args) == 0 || (args[0] != "__setup" && args[0] != "__onboard") {
		return func() {}
	}
	id, err := verbs.LoadIdentity()
	if err != nil || id != nil || activeOperatorRequest != "" {
		return func() {}
	}
	operatorOwnerDepth++
	return func() { operatorOwnerDepth-- }
}

type operatorBridgeRequest struct {
	ID       string          `json:"id"`
	Command  []string        `json:"command,omitempty"`
	Input    string          `json:"input,omitempty"`
	Release  string          `json:"release,omitempty"`
	Origin   string          `json:"origin,omitempty"`
	ExitCode int             `json:"exit_code,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// operatorHelperArgs preserves a checked request across the host/helper boundary.
func operatorHelperArgs(args []string) []string {
	if activeOperatorRequest == "" || len(args) == 0 || strings.HasPrefix(args[0], "__operator-") {
		return args
	}
	return append([]string{"__operator-forward", activeOperatorRequest, "--"}, args...)
}

func operatorRequestFlag(args []string) ([]string, string, error) {
	clean := make([]string, 0, len(args))
	request := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg != "--request-id" && !strings.HasPrefix(arg, "--request-id=") {
			clean = append(clean, arg)
			continue
		}
		if request != "" {
			return nil, "", verbs.Usagef("Supply one request identifier.")
		}
		if strings.HasPrefix(arg, "--request-id=") {
			request = strings.TrimPrefix(arg, "--request-id=")
		} else {
			i++
			if i == len(args) {
				return nil, "", verbs.Usagef("The request identifier is absent.")
			}
			request = args[i]
		}
		if request == "" {
			return nil, "", verbs.Usagef("The request identifier is absent.")
		}
	}
	return clean, request, nil
}

func operatorBody(args []string, d operatorcmd.Definition, stdin io.Reader) (string, error) {
	needsInput := strings.Contains(d.RequestSource, "stdin")
	for i, arg := range args {
		if arg == "--request" && i+1 < len(args) && args[i+1] != "-" {
			needsInput = false
		}
		if strings.HasPrefix(arg, "--request=") && arg != "--request=-" {
			needsInput = false
		}
	}
	if len(args) > 2 && args[0] == "project" && (args[1] == "vision-draft" || args[1] == "vision-edit") {
		needsInput = false
	}
	if !needsInput || stdin == nil {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(stdin, verbs.OperatorRequestLimit+1))
	if err != nil || len(body) > verbs.OperatorRequestLimit {
		return "", verbs.Usagef("The operator request exceeds its input limit.")
	}
	return string(body), nil
}

func operatorPreDispatch(args []string, stdin io.Reader, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	if args[0] == "__operator-forward" {
		return true, runOperatorForward(args[1:], stdin, stdout, stderr)
	}
	if args[0] == "__operator-execute" {
		return true, runOperatorExecute(args[1:], stdout, stderr)
	}
	if strings.HasPrefix(args[0], "__operator-") {
		return true, runOperatorPrivate(args, stdin, stdout, stderr)
	}
	if args[0] == "operation" {
		return true, runOperatorRead(args[1:], stdout, stderr)
	}
	if activeOperatorRequest != "" || operatorOwnerDepth > 0 || operatorReadDepth > 0 {
		return false, 0
	}
	clean, request, err := operatorRequestFlag(args)
	if err != nil {
		return true, writeVerbError(stdout, stderr, err)
	}
	d, ok := operatorcmd.Lookup(clean)
	if !ok {
		return false, 0
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return true, writeVerbError(stdout, stderr, err)
	}
	if id != nil && id.Tier != "collie" {
		return false, 0
	}
	if !d.Mutates && (id == nil || !d.Host) {
		if request != "" {
			return true, writeVerbError(stdout, stderr, verbs.Usagef("This read requires no request identifier."))
		}
		reader, err := validateOperatorCommand(clean, stdin)
		if err != nil {
			return true, writeVerbError(stdout, stderr, err)
		}
		if id != nil && d.Permission != "" && clean[0] != "collie" {
			body, err := operatorBody(clean, d, reader)
			if err != nil {
				return true, writeVerbError(stdout, stderr, err)
			}
			if err := operatorSpineCall("read-check", operatorBridgeRequest{Command: clean, Input: body}, nil); err != nil {
				return true, writeVerbError(stdout, stderr, err)
			}
			reader = strings.NewReader(body)
		}
		operatorReadDepth++
		defer func() { operatorReadDepth-- }()
		return true, dispatchArgs(clean, reader, stdout, stderr)
	}
	stdin, err = validateOperatorCommand(clean, stdin)
	if err != nil {
		return true, writeVerbError(stdout, stderr, err)
	}
	// Host component calls retain their existing synchronous contracts.
	// Agent and dashboard writes receive a durable receipt before any effect.
	if id == nil {
		if err := operatorDeploymentGuard(d.Mutates, releaseBuildID); err != nil {
			return true, writeVerbError(stdout, stderr, err)
		}
	}
	if id != nil && id.ReleaseBuildID != "" && id.ReleaseBuildID != releaseBuildID {
		return true, writeVerbError(stdout, stderr, verbs.Domainf("The Collie runtime and command use different releases."))
	}
	body, err := operatorBody(clean, d, stdin)
	if err != nil {
		return true, writeVerbError(stdout, stderr, err)
	}
	if request == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return true, writeVerbError(stdout, stderr, err)
		}
		request = "op-" + hex.EncodeToString(random[:])
	}
	origin := "external"
	if id == nil && (os.Getenv("MC_OPERATOR_ORIGIN") == "dashboard" || os.Getenv("MC_OPERATOR_ORIGIN") == "component") {
		origin = os.Getenv("MC_OPERATOR_ORIGIN")
	}
	var receipt verbs.OperatorRequest
	err = operatorSpineCall("submit", operatorBridgeRequest{ID: request, Command: clean, Input: body, Release: releaseBuildID, Origin: origin}, &receipt)
	if err != nil {
		return true, writeVerbError(stdout, stderr, err)
	}
	if id == nil && receipt.State == "pending" {
		var response bytes.Buffer
		if code := runOperatorExecute([]string{request}, &response, stderr); code != 0 {
			_, _ = stdout.Write(response.Bytes())
			return true, code
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := operatorSpineCall("get", operatorBridgeRequest{ID: request}, &receipt); err != nil {
			return true, writeVerbError(stdout, stderr, verbs.Domainf("The operation status is unavailable. Inspect operation %s before another request.", request))
		}
		if receipt.State == "complete" && receipt.ExitCode != nil {
			_, err := stdout.Write(append(receipt.Result, '\n'))
			if err != nil {
				return true, 2
			}
			if receipt.Error != "" {
				fmt.Fprintln(stderr, receipt.Error)
			}
			return true, *receipt.ExitCode
		}
		if receipt.State == "uncertain" || time.Now().After(deadline) {
			if err := writeJSON(stdout, map[string]any{"operation": receipt.Public(), "accepted": true, "complete": false}); err != nil {
				return true, 2
			}
			return true, 0
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func operatorSpineCall(action string, request operatorBridgeRequest, response any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	var out, diagnostic bytes.Buffer
	args := []string{"__operator-" + action}
	var code int
	if shouldDelegateToHelper() {
		if _, err := ensureRuntimeHelper(); err != nil {
			return err
		}
		code = delegate(args, bytes.NewReader(body), &out, &diagnostic)
	} else {
		code = runOperatorPrivate(args, bytes.NewReader(body), &out, &diagnostic)
	}
	if code != 0 {
		return operatorSpineError(code, out.Bytes(), diagnostic.String())
	}
	if response != nil && json.Unmarshal(out.Bytes(), response) != nil {
		return verbs.Usagef("The operator request returned an invalid response.")
	}
	return nil
}

func operatorSpineError(code int, output []byte, diagnostic string) error {
	if err := helperRefusal(output); err != nil {
		if code == 2 {
			return verbs.Usagef("%s", err.Msg)
		}
		return err
	}
	message := "The operator request failed: " + strings.TrimSpace(diagnostic)
	if code == 2 {
		return verbs.Usagef("%s", message)
	}
	return verbs.Domainf("%s", message)
}

func runOperatorPrivate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("The operator transport request is invalid."))
	}
	action := strings.TrimPrefix(args[0], "__operator-")
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if action != "submit" && action != "get" && action != "list" && action != "read-check" {
		if err := verbs.RequireHostScope(id, "Operator transport control"); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	if shouldDelegateToHelper() {
		return delegate(args, stdin, stdout, stderr)
	}
	var request operatorBridgeRequest
	if stdin != nil {
		dec := json.NewDecoder(io.LimitReader(stdin, 2*verbs.OperatorRequestLimit+1))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&request); err != nil && err != io.EOF {
			return writeVerbError(stdout, stderr, verbs.Usagef("The operator transport body is invalid."))
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return writeVerbError(stdout, stderr, verbs.Usagef("The operator transport body contains extra data."))
		}
	}
	result, err := withSpine(func(db *sql.DB) (any, error) {
		switch action {
		case "submit":
			// The executable selects its release. A run cannot supply another release.
			if request.Release != releaseBuildID {
				return nil, verbs.Domainf("The operator request uses another release.")
			}
			return verbs.OperatorRequestSubmit(db, id, request.ID, request.Command, request.Input, request.Release, request.Origin)
		case "get":
			return verbs.OperatorRequestGet(db, id, request.ID)
		case "read-check":
			return map[string]bool{"authorized": true}, verbs.AuthorizeOperatorRead(db, id, request.Command, request.Input)
		case "list", "due":
			rows, err := verbs.OperatorRequestList(db, id, action == "due")
			return map[string]any{"operations": rows}, err
		case "claim":
			return verbs.OperatorRequestClaim(db, id, request.ID)
		case "authorize":
			return verbs.OperatorRequestAuthorize(db, id, request.ID)
		case "complete":
			return map[string]bool{"complete": true}, verbs.OperatorRequestComplete(db, id, request.ID, request.ExitCode, request.Result, request.Error)
		case "refuse":
			return verbs.OperatorRequestRefuse(db, id, request.ID, request.ExitCode, request.Result, request.Error)
		case "recover":
			return map[string]bool{"recovered": true}, verbs.OperatorRequestRecover(db, id, request.ID)
		default:
			return nil, verbs.Usagef("The operator transport action is unknown.")
		}
	})
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err = writeJSON(stdout, result); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}

func runOperatorRead(args []string, stdout, stderr io.Writer) int {
	if len(args) == 2 && args[0] == "get" {
		var result verbs.OperatorRequest
		if err := operatorSpineCall("get", operatorBridgeRequest{ID: args[1]}, &result); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := writeJSON(stdout, map[string]any{"operation": result.Public()}); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return 0
	}
	if len(args) == 1 && args[0] == "list" {
		var result struct {
			Operations []verbs.OperatorRequest `json:"operations"`
		}
		if err := operatorSpineCall("list", operatorBridgeRequest{}, &result); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		for i := range result.Operations {
			result.Operations[i] = result.Operations[i].Public()
		}
		if err := writeJSON(stdout, result); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return 0
	}
	return writeVerbError(stdout, stderr, verbs.Usagef("Use mc operation --help for operation status."))
}

func runOperatorForward(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	id, err := verbs.LoadIdentity()
	if err == nil {
		err = verbs.RequireHostScope(id, "Operator helper context")
	}
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if len(args) < 3 || args[1] != "--" || !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr, verbs.Domainf("The operator context requires the deployment helper."))
	}
	var receipt verbs.OperatorRequest
	if err := operatorSpineCall("authorize", operatorBridgeRequest{ID: args[0]}, &receipt); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if receipt.Release != releaseBuildID {
		return writeVerbError(stdout, stderr, verbs.Domainf("The request and helper use different Culler releases."))
	}
	previous := activeOperatorRequest
	activeOperatorRequest = args[0]
	defer func() { activeOperatorRequest = previous }()
	code, err := verbs.WithOperatorRequest(args[0], func() int { return dispatchOperatorCommand(receipt, args[2:], stdin, stdout, stderr) })
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return code
}

func dispatchOperatorCommand(request verbs.OperatorRequest, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if request.Identity != nil && slices.Equal(args, request.Command) && len(args) == 2 && args[0] == "agent-profile" && (args[1] == "operation-status" || args[1] == "operation-cancel") {
		var body struct {
			OperationID string `json:"operation_id"`
		}
		if json.Unmarshal([]byte(request.Input), &body) != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("The operation request is invalid."))
		}
		home, err := configuredCanonicalHome()
		if err == nil {
			var kind string
			kind, err = deployment.AgentProfileOperationKind(home, body.OperationID)
			if err == nil && kind != "model_test" && kind != "opencode_download" {
				err = verbs.Domainf("Collie cannot inspect or cancel a credential authorization.")
			}
		}
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	d, ok := operatorcmd.Lookup(args)
	if !ok || d.Host || shouldDelegateToHelper() || request.Identity == nil || !slices.Equal(args, request.Command) {
		return dispatchArgs(args, stdin, stdout, stderr)
	}
	code := 0
	_, err := withSpine(func(db *sql.DB) (any, error) {
		var err error
		code, err = verbs.WithOperatorCaller(db, request.ID, func() int { return dispatchArgs(args, stdin, stdout, stderr) })
		return nil, err
	})
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return code
}

func runOperatorExecute(args []string, stdout, stderr io.Writer) int {
	id, err := verbs.LoadIdentity()
	if err == nil {
		err = verbs.RequireHostScope(id, "Operator host execution")
	}
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("An operator request identifier is required."))
	}
	var request verbs.OperatorRequest
	if err := operatorSpineCall("claim", operatorBridgeRequest{ID: args[0]}, &request); err != nil {
		var output bytes.Buffer
		code := writeVerbError(&output, stderr, err)
		var retained verbs.OperatorRequest
		if finishErr := operatorSpineCall("refuse", operatorBridgeRequest{ID: args[0], ExitCode: code, Result: output.Bytes(), Error: err.Error()}, &retained); finishErr != nil {
			return writeVerbError(stdout, stderr, finishErr)
		}
		if err := writeJSON(stdout, map[string]any{"operation": retained.Public()}); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return 0
	}
	if !request.Claimed {
		if err := writeJSON(stdout, map[string]any{"operation": request.Public()}); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return 0
	}
	var output, diagnostic bytes.Buffer
	code := 0
	d, ok := operatorcmd.Lookup(request.Command)
	if !ok {
		code = writeVerbError(&output, &diagnostic, verbs.Domainf("The command is outside the supported operator surface."))
	} else if err := operatorDeploymentGuard(d.Mutates, request.Release); err != nil {
		code = writeVerbError(&output, &diagnostic, err)
	} else {
		previous := activeOperatorRequest
		activeOperatorRequest = request.ID
		code, err = verbs.WithOperatorRequest(request.ID, func() int {
			return dispatchOperatorCommand(request, request.Command, strings.NewReader(request.Input), &output, &diagnostic)
		})
		activeOperatorRequest = previous
		if err != nil {
			code = writeVerbError(&output, &diagnostic, err)
		}
	}
	if !json.Valid(output.Bytes()) || output.Len() > verbs.OperatorRequestLimit {
		if err := operatorSpineCall("recover", operatorBridgeRequest{ID: request.ID}, nil); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := writeJSON(stdout, map[string]any{"id": request.ID, "state": "uncertain"}); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return 0
	}
	if err := operatorSpineCall("complete", operatorBridgeRequest{ID: request.ID, ExitCode: code, Result: output.Bytes(), Error: diagnostic.String()}, nil); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, map[string]any{"id": request.ID, "state": "complete", "exit_code": code}); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}
