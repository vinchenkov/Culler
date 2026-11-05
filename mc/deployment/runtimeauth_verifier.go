package deployment

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	operatorconfig "mc/config"
	"mc/routing"
)

const (
	runtimeAuthNoopPrompt = "Reply with exactly MC_RUNTIME_AUTH_OK. Do not use tools."
	liveProviderArtifact  = "mc-live-provider-tool.txt"
	liveProviderPrompt    = "Use the shell tool exactly once to run this exact command:\nprintf 'MC_LIVE_PROVIDER_TOOL_OK' | tee mc-live-provider-tool.txt\nAfter it succeeds, reply with exactly MC_LIVE_PROVIDER_TURN_OK. Do not use any other tools."
)

type runtimeAuthOutput struct {
	Stdout []byte
	Stderr []byte
}
type runtimeAuthRunFunc func(argv []string, stdin string) (runtimeAuthOutput, error)

type boundedRuntimeAuthBuffer struct {
	mu       sync.Mutex
	body     []byte
	limit    int
	overflow bool
}

func (b *boundedRuntimeAuthBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.body)
	if remaining > 0 {
		take := len(p)
		if take > remaining {
			take = remaining
		}
		b.body = append(b.body, p[:take]...)
	}
	if len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

// AdapterNoopVerifier tests access through the production adapter and its native session path.
// RunnerSourceDir identifies an installed runner release.
// An empty Catalog loads the operator catalog from Home.
type AdapterNoopVerifier struct {
	// Context cancels host authorization and Docker commands.
	// A nil context uses the background context.
	// Custom callbacks must handle context cancellation.
	Context          context.Context
	Image            string
	RunnerSourceDir  string
	Home             string
	Catalog          RuntimeCatalog
	Access           func(connection, profile, model string, maximum int) (*providerAccessLease, error)
	Run              runtimeAuthRunFunc
	RequireToolProof bool
	// Setup selects the default for an unnamed, disabled reference profile.
	SelectDefaultModel    bool
	DefaultModel          func(context.Context, string, string) (string, error)
	defaultModelSelection bool
}

// RuntimeAuthCleanupError identifies a test container that can remain active after cancellation.
type RuntimeAuthCleanupError struct{}

func (*RuntimeAuthCleanupError) Error() string {
	return "The credential test could not remove its scratch container. Ask your coding harness to remove the test container, then retry setup."
}

// NewAdapterNoopVerifier tests connections in the agent image of the deployment.
func NewAdapterNoopVerifier(home, image string) *AdapterNoopVerifier {
	return &AdapterNoopVerifier{
		Image:           image,
		RunnerSourceDir: filepath.Join(home, "release", "runner"),
		Home:            home,
	}
}

// VerificationContext lets selected credential imports cancel the lock wait and reject publication.
func (v *AdapterNoopVerifier) VerificationContext() context.Context {
	if v.Context != nil {
		return v.Context
	}
	return context.Background()
}

func defaultRuntimeAuthRunContext(ctx context.Context, argv []string, stdin string) (runtimeAuthOutput, error) {
	if err := ctx.Err(); err != nil {
		return runtimeAuthOutput{}, err
	}
	if len(argv) == 0 || argv[0] != "run" {
		return runRuntimeAuthDocker(ctx, argv, stdin)
	}
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return runtimeAuthOutput{}, err
	}
	name := fmt.Sprintf("mc-runtime-auth-verify-%x", identity)
	args := append([]string{"run", "--name", name}, argv[1:]...)
	output, err := runRuntimeAuthDocker(ctx, args, stdin)
	if err != nil {
		// Docker can retain the container after its client stops.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		cleanup, cleanupErr := runRuntimeAuthDocker(cleanupCtx, []string{"rm", "--force", name}, "")
		if cleanupErr != nil && !bytes.Contains(cleanup.Stderr, []byte("No such container: "+name)) {
			err = errors.Join(err, &RuntimeAuthCleanupError{})
		}
	}
	return output, err
}

func runRuntimeAuthDocker(parent context.Context, argv []string, stdin string) (runtimeAuthOutput, error) {
	var output runtimeAuthOutput
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", argv...)
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Stdin = strings.NewReader(stdin)
	stdout := &boundedRuntimeAuthBuffer{limit: 16 * 1024 * 1024}
	stderr := &boundedRuntimeAuthBuffer{limit: 16 * 1024 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if parent.Err() != nil {
		return output, parent.Err()
	}
	if ctx.Err() != nil {
		return output, fmt.Errorf("adapter no-op exceeded two minutes: %w", ctx.Err())
	}
	if stdout.overflow || stderr.overflow {
		return output, fmt.Errorf("adapter output exceeded 16 MiB per stream")
	}
	output.Stdout, output.Stderr = stdout.body, stderr.body
	return output, err
}

// runtimeAuthAdapterDiagnostic renders the tail of a failed adapter run with
// every known credential redacted. Without it the operator is told only that
// the adapter exited nonzero, which names no cause and suggests no action.
// Redaction is unconditional here: the live-mode leak scan downstream never
// runs on this path because the failure returns first.
func runtimeAuthAdapterDiagnostic(out runtimeAuthOutput, sensitive []string) string {
	const tail = 2 * 1024
	body := out.Stderr
	if len(bytes.TrimSpace(body)) == 0 {
		body = out.Stdout
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return "adapter produced no output"
	}
	if len(body) > tail {
		body = body[len(body)-tail:]
	}
	rendered := string(body)
	for _, secret := range sensitive {
		if secret != "" {
			rendered = strings.ReplaceAll(rendered, secret, "[redacted]")
		}
	}
	return strings.TrimSpace(rendered)
}

// RuntimeAuthAdapterError carries the condition that a failed adapter reported.
type RuntimeAuthAdapterError struct {
	Condition  string
	Diagnostic string
}

func (e *RuntimeAuthAdapterError) Error() string {
	return "production adapter exited nonzero: " + e.Diagnostic
}

// runtimeAuthAdapterCondition returns the redacted condition of the last
// infrastructure-failure event that the adapter wrote to stdout.
func runtimeAuthAdapterCondition(out runtimeAuthOutput, sensitive []string) string {
	condition := ""
	for _, line := range bytes.Split(out.Stdout, []byte{'\n'}) {
		var event struct {
			Event     string `json:"event"`
			Condition string `json:"condition"`
		}
		if json.Unmarshal(line, &event) == nil && event.Event == "infrastructure-failure" && strings.TrimSpace(event.Condition) != "" {
			condition = strings.TrimSpace(event.Condition)
		}
	}
	for _, secret := range sensitive {
		if secret != "" {
			condition = strings.ReplaceAll(condition, secret, "[redacted]")
		}
	}
	return condition
}

// TokenRefreshError reports a token refresh that the provider refused. Code is
// the provider error code of the reply, or empty.
type TokenRefreshError struct {
	Status int
	Code   string
}

func (e *TokenRefreshError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("token endpoint refused with HTTP %d", e.Status)
	}
	return fmt.Sprintf("token endpoint refused with HTTP %d: %s", e.Status, e.Code)
}

func replaceRuntimeGrant(path string, grant any) error {
	body, err := json.MarshalIndent(grant, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	next := path + ".next"
	f, err := os.OpenFile(next, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(next)
		return err
	}
	if err := os.Rename(next, path); err != nil {
		_ = os.Remove(next)
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func sessionStartLocator(output []byte) (string, bool) {
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		var event struct {
			Event      string `json:"event"`
			SessionID  string `json:"session_id"`
			NativeFile string `json:"native_file"`
		}
		if json.Unmarshal(line, &event) == nil && event.Event == "session-start" && event.SessionID != "" && event.NativeFile != "" {
			if filepath.IsAbs(event.NativeFile) || strings.Contains(event.NativeFile, "\\") {
				return "", false
			}
			clean := filepath.Clean(event.NativeFile)
			if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean != event.NativeFile {
				return "", false
			}
			return clean, true
		}
	}
	return "", false
}

func runtimeAuthNoopSucceeded(output []byte) bool {
	complete := 0
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		var event struct {
			Event  string `json:"event"`
			Status string `json:"status"`
			Output string `json:"output"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if event.Event == "infrastructure-failure" {
			return false
		}
		if event.Event == "turn-complete" {
			if event.Status != "success" || strings.TrimSpace(event.Output) != "MC_RUNTIME_AUTH_OK" {
				return false
			}
			complete++
		}
	}
	return complete == 1
}

func (v *AdapterNoopVerifier) execute(
	binding, stagedGrantDir, prompt string,
	live bool,
	attemptID, expectedAuthorizationID string,
) (observation LiveProviderObservation, retErr error) {
	ctx := v.VerificationContext()
	if err := ctx.Err(); err != nil {
		return observation, err
	}
	defer func() {
		if err := ctx.Err(); err != nil {
			observation = LiveProviderObservation{}
			if !errors.Is(retErr, err) {
				retErr = errors.Join(err, retErr)
			}
		}
	}()
	var sensitive []string
	catalog := v.Catalog
	if catalog.Bindings == nil {
		loaded, err := LoadRuntimeCatalog(v.Home)
		if err != nil {
			return observation, err
		}
		catalog = loaded
	}
	if err := catalog.validateBinding(binding, v.defaultModelSelection); err != nil {
		return observation, err
	}
	spec := catalog.Bindings[binding]
	checkAuthorization := func() error {
		if !live {
			return nil
		}
		current, err := AgentConnectionAuthorizationIdentity(v.Home, spec.Credential)
		if err != nil || current != expectedAuthorizationID {
			return fmt.Errorf("The connection authorization changed. Start a new model test.")
		}
		return nil
	}
	if err := checkAuthorization(); err != nil {
		return observation, err
	}
	codex := spec.Channel == "codex"
	if err := ValidateRunnerRelease(v.RunnerSourceDir); err != nil {
		return observation, fmt.Errorf("installed runner source is incomplete: %w", err)
	}
	root, err := os.MkdirTemp(filepath.Dir(stagedGrantDir), ".runtime-auth-verify-")
	if err != nil {
		return observation, err
	}
	defer os.RemoveAll(root)
	runnerDir, err := projectVerificationRunner(v.RunnerSourceDir, root)
	if err != nil {
		return observation, err
	}
	sessionDir, workspaceDir := filepath.Join(root, "session"), filepath.Join(root, "workspace")
	if err := os.Mkdir(sessionDir, 0o700); err != nil {
		return observation, err
	}
	if err := os.Mkdir(workspaceDir, 0o700); err != nil {
		return observation, err
	}
	// The private host parent protects these mounts. The container user requires write access.
	for _, path := range []string{sessionDir, workspaceDir} {
		if err := os.Chmod(path, 0o777); err != nil {
			return observation, err
		}
	}
	image := v.Image
	if image == "" {
		return observation, fmt.Errorf("the connection test requires the agent image of the deployment")
	}
	run := v.Run
	if run == nil {
		run = func(argv []string, stdin string) (runtimeAuthOutput, error) {
			return defaultRuntimeAuthRunContext(ctx, argv, stdin)
		}
	}
	argv := []string{
		"run", "--rm", "-i", "--label", "mc-managed=true", "--label", "mc-tier=runtime-auth-verify",
		// The agent container profile: no harness runs as root.
		"--user", "10002:10002", "--cap-drop", "ALL",
		"-e", "MC_PRIVATE_BUN=" + RuntimeRoot + "/bin/bun", "-e", "MC_PRIVATE_NODE=" + RuntimeRoot + "/bin/node",
		"-v", sessionDir + ":/mc/session", "-v", workspaceDir + ":/workspace/source",
		"-v", runnerDir + ":" + RuntimeRoot + "/app/src:ro",
	}
	adapter := "claude.ts"
	// The adapter is invoked as the production runner invokes it: a credential
	// channel and the binding's model, never a binding id.
	adapterArgs := []string{
		"--session-dir", "/mc/session", "--workspace", "/workspace/source",
		"--channel", spec.Channel,
	}
	if spec.Model != "" {
		adapterArgs = append(adapterArgs, "--model", spec.Model)
	}
	if spec.Harness == "claude-sdk" {
		// The adapter stops after this many turns, so a claude-sdk turn sends
		// at most the bounded number of provider requests on both channels.
		adapterArgs = append(adapterArgs, "--max-turns", strconv.Itoa(maxLiveProviderRequests))
	}
	access := v.Access
	if access == nil {
		access = func(connection, profile, model string, maximum int) (*providerAccessLease, error) {
			return authorizeProviderAccessContext(ctx, v.Home, connection, profile, model, "", maximum, routing.ProfileRevision(spec))
		}
	}
	maximum := 1
	if live || v.RequireToolProof {
		maximum = maxLiveProviderRequests
	}
	lease, err := access(spec.Credential, binding, spec.Model, maximum)
	if err != nil {
		return observation, err
	}
	if lease == nil {
		return observation, fmt.Errorf("The host returned no provider access authorization.")
	}
	defer func() { retErr = errors.Join(retErr, lease.close()) }()
	if err := validateProviderContainerAccess(lease.Config); err != nil {
		return observation, err
	}
	if lease.Config.Harness != spec.Harness {
		return observation, fmt.Errorf("The host provider access harness does not match the selected profile.")
	}
	expectedMode := "account"
	if strings.HasSuffix(spec.AuthKind, "-api-key") {
		expectedMode = "api-key"
	}
	if lease.Config.AuthMode != expectedMode {
		return observation, fmt.Errorf("The host provider access mode does not match the selected connection.")
	}
	sensitive = append(sensitive, lease.Config.Token)
	privateConfig, err := json.Marshal(lease.Config)
	if err != nil {
		return observation, err
	}
	accessPath := filepath.Join(root, "provider-access.json")
	if err := os.WriteFile(accessPath, privateConfig, 0600); err != nil {
		return observation, err
	}
	if err := os.Chmod(accessPath, 0644); err != nil {
		return observation, err
	}
	argv = append(argv, "--add-host", "host.docker.internal:host-gateway",
		"-v", accessPath+":/mc/provider-access.json:ro", "-e", "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json")
	if codex {
		adapter = "codex.ts"
		adapterArgs = []string{"--session-dir", "/mc/session", "--workspace", "/workspace/source", "--mode", "fresh"}
		if spec.Model != "" {
			adapterArgs = append(adapterArgs, "--model", spec.Model)
		}
	}
	if spec.Effort != "" {
		adapterArgs = append(adapterArgs, "--effort", spec.Effort)
	}
	argv = append(argv, image, RuntimeRoot+"/bin/bun", RuntimeRoot+"/app/src/agent-runner/adapters/"+adapter)
	argv = append(argv, adapterArgs...)
	if err := ctx.Err(); err != nil {
		return observation, err
	}
	if err := checkAuthorization(); err != nil {
		return observation, err
	}
	out, err := run(argv, prompt)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return observation, errors.Join(cancelErr, err)
	}
	if err := checkAuthorization(); err != nil {
		return observation, err
	}
	if err != nil {
		failure := &RuntimeAuthAdapterError{
			Condition:  runtimeAuthAdapterCondition(out, sensitive),
			Diagnostic: runtimeAuthAdapterDiagnostic(out, sensitive),
		}
		var cleanup *RuntimeAuthCleanupError
		if errors.As(err, &cleanup) {
			return observation, errors.Join(failure, cleanup)
		}
		return observation, failure
	}
	{
		for _, secret := range sensitive {
			if secret != "" &&
				(bytes.Contains(out.Stdout, []byte(secret)) ||
					bytes.Contains(out.Stderr, []byte(secret))) {
				return observation, fmt.Errorf("production adapter output leaked runtime credential material")
			}
		}
	}
	locator, ok := sessionStartLocator(out.Stdout)
	if !ok {
		return observation, fmt.Errorf("production adapter returned no durable session-start")
	}
	tracePath := filepath.Join(sessionDir, locator)
	resolved, resolveErr := filepath.EvalSymlinks(tracePath)
	sessionResolved, sessionErr := filepath.EvalSymlinks(sessionDir)
	if resolveErr != nil || sessionErr != nil || resolved != filepath.Join(sessionResolved, locator) {
		return observation, fmt.Errorf("The native trace path contains a symbolic link or is unavailable.")
	}
	info, err := os.Lstat(filepath.Join(sessionDir, locator))
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return observation, fmt.Errorf("production adapter did not persist its registered native trace")
	}
	if info.Size() > 16*1024*1024 {
		return observation, fmt.Errorf("The native trace exceeded 16 MiB.")
	}
	trace, err := os.ReadFile(filepath.Join(sessionDir, locator))
	if err != nil {
		return observation, fmt.Errorf("The native trace is unavailable.")
	}
	for _, secret := range sensitive {
		if secret != "" && bytes.Contains(trace, []byte(secret)) {
			return observation, fmt.Errorf("The native trace leaked runtime credential material.")
		}
	}
	requests, err := lease.requestsContext(ctx)
	if err != nil || requests < 1 || requests > maximum {
		return observation, fmt.Errorf("The model test lacks bounded host request evidence.")
	}
	if !live && !v.RequireToolProof {
		if !runtimeAuthNoopSucceeded(out.Stdout) {
			return observation, fmt.Errorf("The connection test returned no successful terminal response.")
		}
		observation.ProviderRequests = requests
		if codex && v.defaultModelSelection {
			model, err := verifiedCodexModel(filepath.Join(sessionDir, locator), out.Stdout)
			if err != nil {
				return observation, err
			}
			if model != spec.Model {
				return observation, fmt.Errorf("The native trace selected a different model.")
			}
			observation.SelectedModel = model
		}
		return observation, nil
	}
	observation, err = InspectLiveProviderOutput(spec.Harness, out.Stdout)
	if err != nil {
		return observation, err
	}
	observation.ProviderRequests = requests
	if observation.NativeFile != locator {
		return observation, fmt.Errorf("provider observation disagrees with its registered native trace")
	}
	artifactPath := filepath.Join(workspaceDir, liveProviderArtifact)
	artifact, err := os.Lstat(artifactPath)
	if err != nil || artifact.Mode()&os.ModeSymlink != 0 ||
		!artifact.Mode().IsRegular() || artifact.Size() != int64(len(liveProviderToolMarker)) {
		return observation, fmt.Errorf("provider tool did not create the exact isolated artifact")
	}
	body, err := os.ReadFile(artifactPath)
	if err != nil || string(body) != liveProviderToolMarker {
		return observation, fmt.Errorf("provider tool artifact content is incorrect")
	}
	return observation, nil
}

func projectVerificationRunner(source, privateRoot string) (string, error) {
	root := filepath.Join(privateRoot, "runner")
	for _, relative := range runnerReleaseDirs {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.Mkdir(path, 0755); err != nil {
			return "", err
		}
		if err := os.Chmod(path, 0755); err != nil {
			return "", err
		}
	}
	for _, relative := range runnerReleaseFiles {
		body, err := readReleaseSource(source, relative)
		if err != nil {
			return "", err
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.WriteFile(path, body, 0644); err != nil {
			return "", err
		}
		if err := os.Chmod(path, 0644); err != nil {
			return "", err
		}
	}
	return root, nil
}

func (v *AdapterNoopVerifier) Verify(binding, stagedGrantDir string) error {
	if err := v.VerificationContext().Err(); err != nil {
		return err
	}
	copy := *v
	if copy.Catalog.Bindings == nil {
		catalog, err := LoadRuntimeCatalog(v.Home)
		if err != nil {
			return err
		}
		copy.Catalog = catalog
	}
	if err := copy.Catalog.validateBinding(binding, true); err != nil {
		return err
	}
	copy.defaultModelSelection = false
	expected := copy.Catalog.Bindings[binding]
	if v.SelectDefaultModel && expected.Channel == "codex" {
		cfg, err := operatorconfig.Load(filepath.Join(v.Home, "config.toml"))
		if err != nil {
			return err
		}
		_, reference := operatorconfig.Default().Runtimes.Bindings[binding]
		if reference && cfg.Runtimes.Bindings[binding].Name == "" && !agentEnabled(cfg, binding) && expected.Model == "" {
			copy.Catalog = CatalogFromRuntimes(cfg.Runtimes)
			expected = copy.Catalog.Bindings[binding]
			selected := expected
			selectModel := v.DefaultModel
			if selectModel == nil {
				selectModel = selectDefaultAgentModel
			}
			selected.Model, err = selectModel(v.VerificationContext(), v.Home, expected.Credential)
			if err != nil {
				return err
			}
			copy.Catalog.Bindings[binding] = selected
			copy.defaultModelSelection = true
		}
	}
	observation, err := copy.execute(binding, stagedGrantDir, runtimeAuthNoopPrompt, false, "", "")
	if err != nil || observation.SelectedModel == "" {
		return err
	}
	_, err = operatorconfig.UpdateAtomic(filepath.Join(v.Home, "config.toml"), func(cfg *operatorconfig.Config) error {
		if err := v.VerificationContext().Err(); err != nil {
			return err
		}
		current := CatalogFromRuntimes(cfg.Runtimes)
		if agentEnabled(*cfg, binding) || !reflect.DeepEqual(current.Bindings[binding], expected) || current.Credentials[expected.Credential] != copy.Catalog.Credentials[expected.Credential] {
			return fmt.Errorf("The connection configuration changed. Retry the connection.")
		}
		profile := cfg.Runtimes.Bindings[binding]
		profile.Model = observation.SelectedModel
		cfg.Runtimes.Bindings[binding] = profile
		return nil
	})
	return err
}

func (v *AdapterNoopVerifier) VerifyLive(
	binding, canonicalGrantDir, attemptID, expectedAuthorizationID string,
) (LiveProviderObservation, error) {
	if !validSHA256(attemptID) {
		return LiveProviderObservation{}, fmt.Errorf("live verifier requires the exact acceptance attempt id")
	}
	return v.execute(
		binding, canonicalGrantDir, liveProviderPrompt, true,
		attemptID, expectedAuthorizationID)
}

// VerifyModel proves a model response and one successful tool call.
func (v *AdapterNoopVerifier) VerifyModel(binding, grantDir string) (LiveProviderObservation, error) {
	copy := *v
	copy.RequireToolProof = true
	return copy.execute(binding, grantDir, liveProviderPrompt, false, "", "")
}
