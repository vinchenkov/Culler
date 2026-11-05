package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	operatorconfig "mc/config"
)

var agentProfileOperationID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{7,127}$`)

type AgentProfileOperationRequest struct {
	OperationID  string              `json:"operation_id"`
	Kind         string              `json:"kind"`
	ConnectionID string              `json:"connection_id,omitempty"`
	Profile      AgentProfileRequest `json:"profile,omitempty"`
	ConfirmSpend bool                `json:"confirm_spend,omitempty"`
}

type AgentProfileOperation struct {
	Profile          *AgentProfileRequest `json:"profile,omitempty"`
	OperationID      string               `json:"operation_id"`
	Kind             string               `json:"kind"`
	ConnectionID     string               `json:"connection_id,omitempty"`
	Status           string               `json:"status"`
	AuthorizationURL string               `json:"authorization_url,omitempty"`
	UserCode         string               `json:"user_code,omitempty"`
	ErrorCode        string               `json:"error_code,omitempty"`
	Message          string               `json:"message,omitempty"`
	Detail           string               `json:"detail,omitempty"`
	StartedAt        string               `json:"started_at"`
	FinishedAt       string               `json:"finished_at,omitempty"`
	ProviderRequests int                  `json:"provider_requests,omitempty"`
}

type agentProfileOperationRecord struct {
	AgentProfileOperation
	Request         AgentProfileOperationRequest `json:"request"`
	RequestDigest   string                       `json:"request_digest"`
	ExecutionDigest string                       `json:"execution_digest,omitempty"`
	AuthorizationID string                       `json:"authorization_id,omitempty"`
	Published       bool                         `json:"published,omitempty"`
}

// These dependencies keep authorization and model requests outside the fast suite.
type agentProfileOperationDependencies struct {
	Launch    func(home, id string) error
	Authorize func(context.Context, string, string, func(string, string)) (any, error)
	Test      func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error)
	Verify    func(home, connection string) error
}

func agentProfileOperationsRoot(home string) (string, error) {
	canonical, err := CanonicalHome(home)
	if err != nil {
		return "", err
	}
	if err = requireOwnerOnlyDirectory(canonical, "operator home"); err != nil {
		return "", err
	}
	root := filepath.Join(canonical, "agent-profile-operations")
	if err = ensureOwnerDirectory(root, "agent profile operations"); err != nil {
		return "", err
	}
	return root, nil
}

func agentProfileOperationPath(root, id string) (string, error) {
	if !agentProfileOperationID.MatchString(id) {
		return "", fmt.Errorf("The operation identifier is invalid. Start a new operation.")
	}
	return filepath.Join(root, id+".json"), nil
}

func agentProfileOperationLock(root, name string) (*os.File, error) {
	return agentProfileOperationLockContext(nil, root, name)
}

func agentProfileOperationLockContext(ctx context.Context, root, name string) (*os.File, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	path := filepath.Join(root, name+".lock")
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		f.Close()
		return nil, fmt.Errorf("The operation lock is unavailable.")
	}
	if err = waitAgentProfileOperationLock(ctx, fd); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func waitAgentProfileOperationLock(ctx context.Context, fd int) error {
	if ctx == nil {
		return syscall.Flock(fd, syscall.LOCK_EX)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err == syscall.EINTR {
			continue
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func agentProfileUnlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }

func readAgentProfileOperation(root, id string) (agentProfileOperationRecord, error) {
	var record agentProfileOperationRecord
	path, err := agentProfileOperationPath(root, id)
	if err != nil {
		return record, err
	}
	body, err := readSecretSource(path, "agent profile operation")
	if err != nil {
		return record, fmt.Errorf("The operation is unavailable. Refresh the panel.")
	}
	if err = json.Unmarshal(body, &record); err != nil || record.OperationID != id {
		return record, fmt.Errorf("The operation record is invalid.")
	}
	return record, nil
}
func writeAgentProfileOperation(root string, record agentProfileOperationRecord) error {
	path, err := agentProfileOperationPath(root, record.OperationID)
	if err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(root, ".operation-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(body, '\n')); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(root)
}
func agentProfileDigest(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func agentProfileTerminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled"
}

func defaultAgentProfileLaunch(home, id string) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(binary, "agent-profile", "operation-run", "--home", home, "--id", id)
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	sink, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer sink.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = sink, sink, sink
	if err = cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func StartAgentProfileOperation(home string, req AgentProfileOperationRequest) (AgentProfileOperation, error) {
	return startAgentProfileOperation(home, req, agentProfileOperationDependencies{})
}
func startAgentProfileOperation(home string, req AgentProfileOperationRequest, deps agentProfileOperationDependencies) (AgentProfileOperation, error) {
	var empty AgentProfileOperation
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return empty, err
	}
	if _, err = agentProfileOperationPath(root, req.OperationID); err != nil {
		return empty, err
	}
	if req.Kind != "authorization" && req.Kind != "model_test" {
		return empty, fmt.Errorf("Select an authorization or a model test.")
	}
	if req.Kind == "model_test" && !req.ConfirmSpend {
		return empty, fmt.Errorf("Confirm the model test charges before you start the test.")
	}
	lock, err := agentProfileOperationLock(root, req.OperationID)
	if err != nil {
		return empty, err
	}
	defer agentProfileUnlock(lock)
	digest := agentProfileDigest(req)
	path, _ := agentProfileOperationPath(root, req.OperationID)
	if _, err = os.Lstat(path); err == nil {
		previous, err := readAgentProfileOperation(root, req.OperationID)
		if err != nil {
			return empty, err
		}
		if previous.RequestDigest != digest {
			return empty, fmt.Errorf("This operation identifier belongs to another request. Start a new operation.")
		}
		return previous.AgentProfileOperation, nil
	} else if !os.IsNotExist(err) {
		return empty, err
	}
	record := agentProfileOperationRecord{AgentProfileOperation: AgentProfileOperation{OperationID: req.OperationID, Kind: req.Kind, ConnectionID: req.ConnectionID, Status: "pending", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}, Request: req, RequestDigest: digest}
	if req.Kind == "model_test" {
		draft := req.Profile
		draft.Enabled = false
		draft.TestID = ""
		record.Profile = &draft
		catalog, binding, err := ResolveAgentProfile(home, req.Profile)
		if err != nil {
			return empty, err
		}
		record.ConnectionID = catalog.Bindings[binding].Credential
		record.ExecutionDigest = agentProfileExecutionDigest(catalog, binding)
		record.AuthorizationID, err = agentProfileAuthorizationID(home, catalog, record.ConnectionID)
		if err != nil {
			return empty, err
		}
	} else {
		catalog, err := LoadRuntimeCatalog(home)
		if err != nil {
			return empty, err
		}
		source, ok := catalog.Credentials[req.ConnectionID]
		if !ok {
			return empty, fmt.Errorf("Select a saved connection before authorization.")
		}
		if agentAPIKeyKind(source.Kind) {
			return empty, fmt.Errorf("Replace the API key through the connection form.")
		}
		available, message := AgentProfileAuthorizationCapability(source.Kind)
		if !available && deps.Authorize == nil {
			return empty, errors.New(message)
		}
		record.ExecutionDigest = agentProfileDigest(source)
		record.AuthorizationID, err = AgentConnectionAuthorizationIdentity(home, req.ConnectionID)
		if err != nil {
			return empty, err
		}
	}
	if err = writeAgentProfileOperation(root, record); err != nil {
		return empty, err
	}
	launch := deps.Launch
	if launch == nil {
		launch = defaultAgentProfileLaunch
	}
	if err = launch(home, req.OperationID); err != nil {
		record.Status = "failed"
		record.ErrorCode = "unavailable"
		record.Message = "The host could not start this operation. Retry from the panel."
		record.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err = writeAgentProfileOperation(root, record); err != nil {
			return empty, err
		}
	}
	return record.AgentProfileOperation, nil
}

func agentProfileExecutionDigest(catalog RuntimeCatalog, binding string) string {
	spec := catalog.Bindings[binding]
	source := catalog.Credentials[spec.Credential]
	// Names describe the configuration but do not change its execution.
	source.Name = ""
	return agentProfileDigest(struct {
		Binding any
		Source  any
	}{spec, source})
}
func agentProfileAuthorizationID(home string, catalog RuntimeCatalog, id string) (string, error) {
	source, ok := catalog.Credentials[id]
	if !ok {
		return "", fmt.Errorf("The connection is unavailable. Select another connection.")
	}
	if !agentConnectionKind(source.Kind) {
		return "", fmt.Errorf("The connection type is unsupported.")
	}
	identity, err := AgentConnectionAuthorizationIdentity(home, id)
	if err != nil || identity == "" {
		return "", fmt.Errorf("The connection requires access. Reconnect before the model test.")
	}
	return identity, nil
}

func GetAgentProfileOperation(home, id string) (AgentProfileOperation, error) {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return AgentProfileOperation{}, err
	}
	if _, err = agentProfileOperationPath(root, id); err != nil {
		return AgentProfileOperation{}, err
	}
	lock, err := agentProfileOperationLock(root, id)
	if err != nil {
		return AgentProfileOperation{}, err
	}
	defer agentProfileUnlock(lock)
	record, err := readAgentProfileOperation(root, id)
	if err != nil {
		return AgentProfileOperation{}, err
	}
	if !agentProfileTerminal(record.Status) {
		started, _ := time.Parse(time.RFC3339Nano, record.StartedAt)
		if time.Since(started) > 30*time.Second {
			f, openErr := os.OpenFile(filepath.Join(root, id+".worker.lock"), os.O_RDWR|os.O_CREATE, 0o600)
			if openErr == nil {
				if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
					record.Status = "failed"
					record.ErrorCode = "interrupted"
					record.Message = "The host stopped this operation. Start an explicit retry."
					if record.Published {
						record.Status, record.ErrorCode = "succeeded", ""
						record.Message = "Authorization succeeded. Access is not verified. Open System health for the current state."
					}
					record.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
					err = writeAgentProfileOperation(root, record)
					_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				}
				_ = f.Close()
			}
		}
	}
	return record.AgentProfileOperation, err
}
func CancelAgentProfileOperation(home, id string) (AgentProfileOperation, error) {
	return updateAgentProfileOperation(home, id, func(record *agentProfileOperationRecord) error {
		if !agentProfileTerminal(record.Status) && !record.Published {
			record.Status = "cancelled"
			record.Message = "The operation was canceled."
			record.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			record.AuthorizationURL = ""
			record.UserCode = ""
		}
		return nil
	})
}
func updateAgentProfileOperation(home, id string, change func(*agentProfileOperationRecord) error) (AgentProfileOperation, error) {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return AgentProfileOperation{}, err
	}
	if _, err = agentProfileOperationPath(root, id); err != nil {
		return AgentProfileOperation{}, err
	}
	lock, err := agentProfileOperationLock(root, id)
	if err != nil {
		return AgentProfileOperation{}, err
	}
	defer agentProfileUnlock(lock)
	record, err := readAgentProfileOperation(root, id)
	if err == nil {
		err = change(&record)
	}
	if err == nil {
		err = writeAgentProfileOperation(root, record)
	}
	return record.AgentProfileOperation, err
}

// RunAgentProfileOperation runs the model test in the agent image.
// The verified function reports whether the spine records ready access for the connection.
func RunAgentProfileOperation(home, id, image string, verified func(connection string) (bool, error)) error {
	return runAgentProfileOperation(home, id, agentProfileOperationDependencies{
		Test: func(ctx context.Context, home string, catalog RuntimeCatalog, binding string) (LiveProviderObservation, error) {
			return testAgentProfileModel(ctx, home, image, catalog, binding)
		},
		Verify: func(home, connection string) error {
			return verifyAgentConnectionAccess(home, connection, verified)
		},
	})
}
func runAgentProfileOperation(home, id string, deps agentProfileOperationDependencies) error {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return err
	}
	if _, err = agentProfileOperationPath(root, id); err != nil {
		return err
	}
	worker, err := agentProfileOperationLock(root, id+".worker")
	if err != nil {
		return err
	}
	defer agentProfileUnlock(worker)
	var record agentProfileOperationRecord
	_, err = updateAgentProfileOperation(home, id, func(current *agentProfileOperationRecord) error {
		if current.Status != "pending" {
			return io.EOF
		}
		current.Status = "running"
		record = *current
		return nil
	})
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	timeout := 2 * time.Minute
	if record.Kind == "authorization" {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := GetAgentProfileOperation(home, id)
				if err != nil || current.Status == "cancelled" {
					cancel()
					return
				}
			}
		}
	}()
	var observation LiveProviderObservation
	var grant any
	published := false
	releaseOwner := func() {}
	if record.Kind == "authorization" {
		release, ownerErr := AcquireConnectionOwner(ctx, home, record.ConnectionID)
		if ownerErr != nil {
			return ownerErr
		}
		releaseOwner = sync.OnceFunc(release)
		defer releaseOwner()
		authorize := deps.Authorize
		if authorize == nil {
			authorize = authorizeAgentProfileConnection
		}
		grant, err = authorize(ctx, home, record.ConnectionID, func(url, code string) {
			_, _ = updateAgentProfileOperation(home, id, func(current *agentProfileOperationRecord) error {
				if !agentProfileTerminal(current.Status) {
					current.Status = "action_required"
					current.AuthorizationURL = url
					current.UserCode = code
					current.Message = "Complete authorization on the provider page."
				}
				return nil
			})
		})
	} else {
		catalog, binding, resolveErr := ResolveAgentProfile(home, record.Request.Profile)
		err = resolveErr
		if err == nil && agentProfileExecutionDigest(catalog, binding) != record.ExecutionDigest {
			err = fmt.Errorf("configuration changed")
		}
		if err == nil {
			current, digestErr := agentProfileAuthorizationID(home, catalog, record.ConnectionID)
			err = digestErr
			if err == nil && current != record.AuthorizationID {
				err = fmt.Errorf("authorization changed")
			}
		}
		if err == nil {
			observation, err = deps.Test(ctx, home, catalog, binding)
			if err == nil {
				err = validLiveProviderObservation(observation)
			}
			if err == nil && (observation.ProviderRequests < 1 || observation.ProviderRequests > 2) {
				err = fmt.Errorf("the harness exceeded the request limit")
			}
		}
	}
	operationErr := err
	_, err = updateAgentProfileOperation(home, id, func(current *agentProfileOperationRecord) error {
		if agentProfileTerminal(current.Status) {
			return nil
		}
		current.AuthorizationURL = ""
		current.UserCode = ""
		current.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if ctx.Err() != nil {
			operationErr = ctx.Err()
		}
		if operationErr == nil && record.Kind == "authorization" {
			_, operationErr = operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
				if agentProfileDigest(cfg.Runtimes.Credentials[record.ConnectionID]) != record.ExecutionDigest {
					return fmt.Errorf("configuration changed")
				}
				return compareAndPublishAgentConnectionAuthorization(home, record.ConnectionID, record.AuthorizationID, grant)
			})
		}
		if operationErr == nil && record.Kind == "model_test" {
			catalog, binding, resolveErr := ResolveAgentProfile(home, record.Request.Profile)
			operationErr = resolveErr
			if operationErr == nil && agentProfileExecutionDigest(catalog, binding) != record.ExecutionDigest {
				operationErr = fmt.Errorf("configuration changed")
			}
			if operationErr == nil {
				current.AuthorizationID, operationErr = agentProfileAuthorizationID(home, catalog, record.ConnectionID)
				testedIdentity := observation.ProfileAuthorizationID
				if testedIdentity == "" {
					testedIdentity = record.AuthorizationID
				}
				if operationErr == nil && current.AuthorizationID != testedIdentity {
					operationErr = fmt.Errorf("authorization changed")
				}
			}
		}
		if operationErr != nil {
			current.Status = "failed"
			current.ErrorCode, current.Message = agentProfileOperationError(record.Kind, operationErr)
			current.Detail = agentProfileOperationDetail(operationErr)
			return nil
		}
		if record.Kind == "authorization" {
			// The credential is saved. The operation stays active until the access check ends.
			current.Published = true
			current.FinishedAt = ""
			current.Status = "running"
			current.Message = "Authorization succeeded. The access check is in progress."
			published = true
			return nil
		}
		current.Status = "succeeded"
		current.ProviderRequests = observation.ProviderRequests
		current.Message = "The model response and tool call succeeded."
		return nil
	})
	if err != nil || !published {
		return err
	}
	// The resident needs the connection owner for its token refresh.
	releaseOwner()
	verify := deps.Verify
	if verify == nil {
		verify = func(home, connection string) error { return verifyAgentConnectionAccess(home, connection, nil) }
	}
	verifyErr := verify(home, record.ConnectionID)
	_, err = updateAgentProfileOperation(home, id, func(current *agentProfileOperationRecord) error {
		if agentProfileTerminal(current.Status) {
			return nil
		}
		current.Status = "succeeded"
		current.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		current.Message = "Authorization succeeded. Culler verified access to this connection."
		if verifyErr != nil {
			current.Message = "Authorization succeeded. Access is not verified. Open System health for the current state."
		}
		return nil
	})
	return err
}

// A token refresh by the resident records the access state of the new authorization.
// A returned credential alone is no proof, because the resident can return a cached credential.
func verifyAgentConnectionAccess(home, connection string, verified func(string) (bool, error)) error {
	for attempt := 0; ; attempt++ {
		if err := checkProviderAccessViaResident(home, connection); err != nil {
			return err
		}
		if verified == nil {
			return nil
		}
		ready, err := verified(connection)
		if err != nil || ready {
			return err
		}
		if attempt == 2 {
			return fmt.Errorf("the connection access is not verified")
		}
		// A resident reload that started before the credential save reports no change.
		time.Sleep(2 * time.Second)
	}
}

// agentProfileOperationDetail returns the condition that the adapter reported, limited to 1024 bytes.
func agentProfileOperationDetail(err error) string {
	var adapter *RuntimeAuthAdapterError
	if !errors.As(err, &adapter) {
		return ""
	}
	detail := adapter.Condition
	if len(detail) > 1024 {
		detail = strings.ToValidUTF8(detail[:1024], "") + "…"
	}
	return detail
}

func agentProfileOperationError(kind string, err error) (string, string) {
	var native *AuthorizationFailure
	if errors.As(err, &native) {
		return "authorization_" + native.Code, native.Error()
	}
	var effortFailure *AgentModelEffortError
	var notOffered *AgentModelNotOfferedError
	var listFailure *AgentModelListError
	switch {
	case errors.As(err, &effortFailure):
		return "effort", effortFailure.Error()
	case errors.As(err, &notOffered):
		return "model", notOffered.Error()
	case errors.As(err, &listFailure) && (listFailure.Status == 401 || listFailure.Status == 403):
		return "authorization", "The connection could not authorize access. Reconnect and retry."
	case errors.As(err, &listFailure):
		return "unavailable", listFailure.Message
	}
	message := strings.ToLower(err.Error() + "\n" + agentProfileOperationDetail(err))
	switch {
	case strings.Contains(message, "keychain") || strings.Contains(message, "credential store"):
		return "unavailable", "The host credential store is unavailable. Repair the installation with your coding harness, then retry."
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout") || strings.Contains(message, "two minutes"):
		return "timeout", "The request exceeded its time limit. Check the connection and retry."
	case strings.Contains(message, "configuration changed"):
		return "conflict", "The configuration changed during this operation. Refresh the form and retry."
	case kind == "authorization" || strings.Contains(message, "authorization") || strings.Contains(message, "token") || strings.Contains(message, "credential") || strings.Contains(message, "401") || strings.Contains(message, "403"):
		return "authorization", "The connection could not authorize access. Reconnect and retry."
	case strings.Contains(message, "effort"):
		return "effort", "The selected effort is unavailable. Select another effort level and retry the model test."
	case strings.Contains(message, "model") || strings.Contains(message, "404"):
		return "model", "The model is unavailable. Check the model identifier and connection access."
	case strings.Contains(message, "connect") || strings.Contains(message, "network") || strings.Contains(message, "resolve") || strings.Contains(message, "endpoint"):
		return "endpoint", "The agent container could not reach the provider. Check the network connection and retry the model test."
	default:
		return "harness", "The harness could not complete the tool call. Check the connection and model, then retry."
	}
}

func VerifyAgentProfileTest(home, testID string, profile AgentProfileRequest) error {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return err
	}
	record, err := readAgentProfileOperation(root, testID)
	if err != nil {
		return err
	}
	if record.Kind != "model_test" || record.Status != "succeeded" {
		return fmt.Errorf("A successful model test is required before this agent profile can be enabled.")
	}
	catalog, binding, err := ResolveAgentProfile(home, profile)
	if err != nil {
		return err
	}
	digest, err := agentProfileAuthorizationID(home, catalog, catalog.Bindings[binding].Credential)
	if err != nil {
		return err
	}
	if record.ExecutionDigest != agentProfileExecutionDigest(catalog, binding) || record.AuthorizationID != digest {
		return fmt.Errorf("The execution configuration changed. Test this agent profile again.")
	}
	return nil
}

// AgentProfileDiagnosticResult returns a terminal result for the current authorization.
func AgentProfileDiagnosticResult(home, operation string) (*AgentProfileOperation, string, error) {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return nil, "", err
	}
	record, err := readAgentProfileOperation(root, operation)
	if err != nil {
		return nil, "", err
	}
	if record.Kind == "authorization" && record.Status == "failed" {
		auth, err := AgentConnectionAuthorizationIdentity(home, record.ConnectionID)
		if err != nil {
			return nil, "", err
		}
		if auth != record.AuthorizationID {
			return nil, "", nil
		}
		result := record.AgentProfileOperation
		return &result, auth, nil
	}
	if record.Kind != "model_test" || (record.Status != "succeeded" && record.Status != "failed") {
		return nil, "", nil
	}
	catalog, binding, err := ResolveAgentProfile(home, record.Request.Profile)
	if err != nil {
		return nil, "", err
	}
	auth, err := agentProfileAuthorizationID(home, catalog, record.ConnectionID)
	if err != nil {
		return nil, "", err
	}
	if record.AuthorizationID != auth || record.ExecutionDigest != agentProfileExecutionDigest(catalog, binding) {
		return nil, "", nil
	}
	result := record.AgentProfileOperation
	result.Profile = &record.Request.Profile
	return &result, auth, nil
}
