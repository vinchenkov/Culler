package deployment

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	operatorconfig "mc/config"
	"mc/opencodecatalog"
)

const (
	openCodeModelsOperation      = "opencode-models"
	openCodeModelRefreshInterval = time.Hour
	openCodeModelRefreshLimit    = 10 * time.Minute
	openCodeLiveListLimit        = 2 * time.Minute
	maxOpenCodeLiveStoreBytes    = 64 << 20
)

// maxOpenCodeLiveListBytes is the size limit of the live model list.
var maxOpenCodeLiveListBytes int64 = 64 << 20

// The sources of the model list of an OpenCode connection.
const (
	OpenCodeModelSourceCatalog = "release_catalog"
	OpenCodeModelSourceLive    = "release_catalog_and_live"
)

// openCodeLiveModels holds the live models of the latest successful refresh.
type openCodeLiveModels struct {
	Version         int                                `json:"version"`
	CatalogDigest   string                             `json:"catalog_digest"`
	OpenCodeVersion string                             `json:"opencode_version"`
	RefreshedAt     string                             `json:"refreshed_at"`
	Providers       map[string][]opencodecatalog.Model `json:"providers"`
}

// openCodeModelIdentity names the release catalog and the pinned OpenCode of this release.
var openCodeModelIdentity = func() (catalog, version string) {
	return opencodecatalog.Digest(), opencodecatalog.Release().OpenCodeVersion
}

func openCodeLiveModelsPath(home string) string {
	return filepath.Join(home, "state", "opencode-live-models.json")
}

var openCodeLiveCache struct {
	sync.Mutex
	info     os.FileInfo
	identity string
	models   map[string][]opencodecatalog.Model
}

// retainedOpenCodeModels returns the live models of the latest successful refresh.
// It returns no models when another release catalog or another pinned OpenCode derived them.
func retainedOpenCodeModels(home string) (map[string][]opencodecatalog.Model, bool) {
	path := openCodeLiveModelsPath(home)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxOpenCodeLiveStoreBytes {
		return nil, false
	}
	catalog, version := openCodeModelIdentity()
	identity := path + "\x00" + catalog + "\x00" + version
	openCodeLiveCache.Lock()
	defer openCodeLiveCache.Unlock()
	if cached := openCodeLiveCache.info; cached != nil && openCodeLiveCache.identity == identity && os.SameFile(cached, info) &&
		cached.Size() == info.Size() && cached.ModTime().Equal(info.ModTime()) {
		return openCodeLiveCache.models, openCodeLiveCache.models != nil
	}
	openCodeLiveCache.info, openCodeLiveCache.identity, openCodeLiveCache.models = info, identity, nil
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, false
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxOpenCodeLiveStoreBytes+1))
	var store openCodeLiveModels
	if err != nil || len(body) > maxOpenCodeLiveStoreBytes || json.Unmarshal(body, &store) != nil ||
		store.Version != 1 || store.CatalogDigest != catalog || store.OpenCodeVersion != version {
		return nil, false
	}
	models := map[string][]opencodecatalog.Model{}
	for id, retained := range store.Providers {
		provider, ok := opencodecatalog.Find(id)
		if !ok {
			continue
		}
		seen := map[string]bool{}
		for _, model := range retained {
			if !seen[model.ID] && opencodecatalog.ValidLiveModel(provider, model) {
				seen[model.ID] = true
				models[id] = append(models[id], model)
			}
		}
	}
	openCodeLiveCache.models = models
	return models, true
}

func writeOpenCodeLiveModels(home string, store openCodeLiveModels) error {
	path := openCodeLiveModelsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	body, err := json.Marshal(store)
	if err != nil {
		return err
	}
	if len(body) > maxOpenCodeLiveStoreBytes {
		return fmt.Errorf("The live models exceed the size limit.")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".opencode-live-models-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(body); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// OpenCodeProvider returns a release catalog provider with the model list of its connections.
// The model list holds the release catalog models and the retained live models.
func OpenCodeProvider(home, id string) (opencodecatalog.Provider, bool) {
	provider, ok := opencodecatalog.Find(id)
	if !ok {
		return provider, false
	}
	live, _ := retainedOpenCodeModels(home)
	return provider.WithLive(live[id]), true
}

// OpenCodeModelSource names the source of the model lists of the deployment.
func OpenCodeModelSource(home string) string {
	if _, ok := retainedOpenCodeModels(home); ok {
		return OpenCodeModelSourceLive
	}
	return OpenCodeModelSourceCatalog
}

// OpenCodeModelList answers a request for the model list of a release catalog provider.
// The request starts a refresh of the live models when one is due. It returns the available model list at once.
func OpenCodeModelList(home, id string) (provider opencodecatalog.Provider, source string, ok bool) {
	StartOpenCodeModelRefresh(home, false)
	provider, ok = OpenCodeProvider(home, id)
	return provider, OpenCodeModelSource(home), ok
}

// RetainedOpenCodeModel reports a model in the live models that a release retained.
// The live models can belong to another release catalog or another pinned OpenCode.
func RetainedOpenCodeModel(home, provider, model string) bool {
	body, err := os.ReadFile(openCodeLiveModelsPath(home))
	var store struct {
		Providers map[string][]struct {
			ID string `json:"id"`
		} `json:"providers"`
	}
	if err != nil || len(body) > maxOpenCodeLiveStoreBytes || json.Unmarshal(body, &store) != nil {
		return false
	}
	for _, retained := range store.Providers[provider] {
		if retained.ID == model {
			return true
		}
	}
	return false
}

// ValidateOpenCodeProfile checks a model and an effort level against the model list of a provider.
func ValidateOpenCodeProfile(home, providerID, modelID, effort string) error {
	provider, ok := OpenCodeProvider(home, providerID)
	if !ok {
		return fmt.Errorf("The release catalog lacks this provider. Select another provider.")
	}
	return provider.ValidateModel(modelID, effort)
}

// OpenCodeProfileAwaitsRefresh reports a profile that the model list refuses while a release retained its live model.
// Another release catalog or another pinned OpenCode derived that live model, so a successful refresh can restore the profile.
func OpenCodeProfileAwaitsRefresh(home, provider, model, effort string) bool {
	if _, ok := opencodecatalog.Find(provider); !ok || ValidateOpenCodeProfile(home, provider, model, effort) == nil {
		return false
	}
	if _, current := retainedOpenCodeModels(home); current {
		return false
	}
	return RetainedOpenCodeModel(home, provider, model)
}

// RefreshRetainedOpenCodeModels starts a due refresh while an enabled agent profile awaits a refresh.
func RefreshRetainedOpenCodeModels(home string, cfg operatorconfig.Config) {
	for _, id := range cfg.Runtimes.EnabledBindings {
		binding := cfg.Runtimes.Bindings[id]
		source := cfg.Runtimes.Credentials[binding.Credential]
		if source.Kind == operatorconfig.CredentialKindOpenCodeAPIKey && OpenCodeProfileAwaitsRefresh(home, source.CatalogEntry, binding.Model, binding.Effort) {
			StartOpenCodeModelRefresh(home, false)
			return
		}
	}
}

// openCodeModelRefreshLaunch starts the refresh in a separate process.
var openCodeModelRefreshLaunch = defaultAgentProfileLaunch

// StartOpenCodeModelRefresh starts a refresh of the live models after the OpenCode installation.
// Without force, it starts no refresh when a refresh started during the previous hour.
// A refresh that does not start changes no model list.
func StartOpenCodeModelRefresh(home string, force bool) {
	_ = startOpenCodeModelRefresh(home, force, time.Now())
}

func startOpenCodeModelRefresh(home string, force bool, now time.Time) error {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return err
	}
	if download, err := readAgentProfileOperation(root, openCodeOperation); err != nil || download.Status != "succeeded" {
		return nil
	}
	lock, err := agentProfileOperationLock(root, openCodeModelsOperation)
	if err != nil {
		return err
	}
	defer agentProfileUnlock(lock)
	if current, err := readAgentProfileOperation(root, openCodeModelsOperation); err == nil {
		started, _ := time.Parse(time.RFC3339Nano, current.StartedAt)
		age := now.Sub(started)
		if age >= 0 && ((!agentProfileTerminal(current.Status) && age < openCodeModelRefreshLimit) || (!force && age < openCodeModelRefreshInterval)) {
			return nil
		}
	}
	record := agentProfileOperationRecord{AgentProfileOperation: AgentProfileOperation{
		OperationID: openCodeModelsOperation, Kind: "opencode_models", Status: "pending", Message: "The refresh of the live models waits for its start.", StartedAt: now.UTC().Format(time.RFC3339Nano)},
		Request: AgentProfileOperationRequest{OperationID: openCodeModelsOperation, Kind: "opencode_models"}}
	if err := writeAgentProfileOperation(root, record); err != nil {
		return err
	}
	if err := openCodeModelRefreshLaunch(home, openCodeModelsOperation); err != nil {
		record.Status, record.ErrorCode = "failed", "unavailable"
		record.Message = "The host could not start the refresh of the live models."
		record.FinishedAt = now.UTC().Format(time.RFC3339Nano)
		return errors.Join(err, writeAgentProfileOperation(root, record))
	}
	return nil
}

// openCodeModelRefreshActive reports a refresh that waits or runs.
func openCodeModelRefreshActive(home string) bool {
	status, err := GetAgentProfileOperation(home, openCodeModelsOperation)
	return err == nil && !agentProfileTerminal(status.Status)
}

type openCodeModelRefreshDependencies struct {
	Fetch  func(context.Context) ([]byte, error)
	Derive func(ctx context.Context, record []byte) (baseline, live []byte, err error)
}

func runOpenCodeModelRefresh(home, image string) error {
	return runOpenCodeModelRefreshWith(home, openCodeModelRefreshDependencies{
		Fetch: func(ctx context.Context) ([]byte, error) {
			return fetchLiveModelList(ctx, http.DefaultTransport, opencodecatalog.LiveModelListAddress)
		},
		Derive: func(ctx context.Context, record []byte) ([]byte, []byte, error) {
			return deriveOpenCodeModelFacts(ctx, home, image, record, runRuntimeDocker)
		},
	})
}

// runOpenCodeModelRefreshWith runs one refresh. The returned error names the cause of a failed refresh.
func runOpenCodeModelRefreshWith(home string, deps openCodeModelRefreshDependencies) error {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return err
	}
	worker, err := agentProfileOperationLock(root, openCodeModelsOperation+".worker")
	if err != nil {
		return err
	}
	defer agentProfileUnlock(worker)
	_, err = updateAgentProfileOperation(home, openCodeModelsOperation, func(record *agentProfileOperationRecord) error {
		if record.Status != "pending" {
			return io.EOF
		}
		record.Status, record.Message = "running", "Culler reads the live model list."
		return nil
	})
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), openCodeModelRefreshLimit)
	defer cancel()
	refreshErr := refreshOpenCodeLiveModels(ctx, home, deps)
	if refreshErr != nil {
		refreshErr = fmt.Errorf("The refresh of the live models failed. %w", refreshErr)
	}
	_, saveErr := updateAgentProfileOperation(home, openCodeModelsOperation, func(record *agentProfileOperationRecord) error {
		record.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		record.Status, record.ErrorCode, record.Message = "succeeded", "", "The model lists hold the live models."
		if refreshErr != nil {
			record.Status, record.ErrorCode, record.Message = "failed", "opencode_models", refreshErr.Error()
		}
		return nil
	})
	return errors.Join(refreshErr, saveErr)
}

func refreshOpenCodeLiveModels(ctx context.Context, home string, deps openCodeModelRefreshDependencies) error {
	catalog, version := openCodeModelIdentity()
	list, err := deps.Fetch(ctx)
	if err != nil {
		return err
	}
	record, err := opencodecatalog.ReadLiveRecord(list)
	if err != nil {
		return err
	}
	baselineReport, liveReport, err := deps.Derive(ctx, record.Body)
	if err != nil {
		return err
	}
	baseline, err := opencodecatalog.ParseFacts(baselineReport)
	if err != nil {
		return err
	}
	live, err := opencodecatalog.ParseFacts(liveReport)
	if err != nil {
		return err
	}
	models := opencodecatalog.LiveModels(record, baseline, live)
	if err := keepSelectedLiveModels(home, models, live); err != nil {
		return err
	}
	return writeOpenCodeLiveModels(home, openCodeLiveModels{Version: 1, CatalogDigest: catalog, OpenCodeVersion: version,
		RefreshedAt: time.Now().UTC().Format(time.RFC3339Nano), Providers: models})
}

// keepSelectedLiveModels preserves the retained facts of each live model that an enabled agent profile selects.
// It applies to a model that the live model list lacks.
func keepSelectedLiveModels(home string, models map[string][]opencodecatalog.Model, live []opencodecatalog.ProviderFacts) error {
	retained, ok := retainedOpenCodeModels(home)
	if !ok {
		return nil
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return err
	}
	listed := map[string]bool{}
	for _, provider := range live {
		for _, fact := range provider.Models {
			listed[provider.ID+"\x00"+fact.ID] = true
		}
	}
	for _, id := range cfg.Runtimes.EnabledBindings {
		binding := cfg.Runtimes.Bindings[id]
		source := cfg.Runtimes.Credentials[binding.Credential]
		if source.Kind != operatorconfig.CredentialKindOpenCodeAPIKey || listed[source.CatalogEntry+"\x00"+binding.Model] {
			continue
		}
		for _, model := range retained[source.CatalogEntry] {
			if model.ID == binding.Model {
				listed[source.CatalogEntry+"\x00"+binding.Model] = true
				models[source.CatalogEntry] = append(models[source.CatalogEntry], model)
			}
		}
	}
	return nil
}

// fetchLiveModelList requests the live model list. The request holds no key and no data of the deployment.
// It accepts a response only from the origin of the address.
func fetchLiveModelList(ctx context.Context, transport http.RoundTripper, address string) ([]byte, error) {
	target, err := url.Parse(address)
	if err != nil || target.Scheme != "https" || target.Host == "" {
		return nil, fmt.Errorf("The live model list address requires HTTPS.")
	}
	ctx, cancel := context.WithTimeout(ctx, openCodeLiveListLimit)
	defer cancel()
	client := &http.Client{Transport: transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 || next.URL.Scheme != target.Scheme || next.URL.Host != target.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "culler")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("The live model list request exceeded its time limit.")
		}
		return nil, fmt.Errorf("The live model list is unavailable. Check the network connection.")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, fmt.Errorf("The live model list address named another origin.")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("The live model list returned HTTP %d.", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOpenCodeLiveListBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("The live model list request exceeded its time limit.")
		}
		return nil, fmt.Errorf("The live model list response is incomplete.")
	}
	if int64(len(body)) > maxOpenCodeLiveListBytes {
		return nil, fmt.Errorf("The live model list exceeds its size limit.")
	}
	return body, nil
}

// The container has no network, no provider key, and no Project data.
// The pinned OpenCode derives the facts of its own model record first, then the facts of the live record.
const openCodeFactsScript = `set -eu
umask 077
mkdir /tmp/work /tmp/home
tar -xf - -C /tmp/work
program=` + OpenCodeRoot + `/node_modules/@opencode/cli-linux-arm64/bin/opencode
export HOME=/tmp/home XDG_DATA_HOME=/tmp/home/data XDG_CONFIG_HOME=/tmp/home/config XDG_CACHE_HOME=/tmp/home/cache XDG_STATE_HOME=/tmp/home/state
export OPENCODE_DISABLE_MODELS_FETCH=1 OPENCODE_DISABLE_FREE_MODELS=1 OPENCODE_DISABLE_AUTOUPDATE=1 OPENCODE_DISABLE_PROJECT_CONFIG=1 OPENCODE_DISABLE_CLAUDE_CODE=1 OPENCODE_DISABLE_FILEWATCHER=1
cd /tmp/home
version=$("$program" --version </dev/null 2>/dev/null) || version=unavailable
[ "$version" = "opencode v$1" ] || { printf 'The installed OpenCode is %s. This release requires OpenCode %s.\n' "$version" "$1" >&2; exit 1; }
export OPENCODE_CONFIG_CONTENT='{"plugins":["file:///tmp/work/facts"]}'
derive() {
  CULLER_MODEL_FACTS="/tmp/work/$1.json" "$program" api --standalone GET /api/model </dev/null >/dev/null 2>"/tmp/work/$1.log" && [ -s "/tmp/work/$1.json" ] && return
  printf 'OpenCode could not derive the model facts: %s\n' "$(tail -n 1 "/tmp/work/$1.log")" >&2
  exit 1
}
derive baseline
export OPENCODE_MODELS_PATH=/tmp/work/record.json
derive live
for name in baseline live; do printf '\n%s\n' "$2"; cat "/tmp/work/$name.json"; done
printf '\n%s\n' "$2"`

func openCodeFactsArchive(record []byte) (string, error) {
	files := map[string][]byte{"record.json": record}
	for name, body := range opencodecatalog.FactsPlugin() {
		files["facts/"+name] = body
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "facts/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		return "", err
	}
	for _, name := range names {
		if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(files[name]))}); err != nil {
			return "", err
		}
		if _, err := writer.Write(files[name]); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return archive.String(), nil
}

// deriveOpenCodeModelFacts runs the installed pinned OpenCode on the live record.
// The OpenCode installation waits while the container reads the OpenCode volume.
func deriveOpenCodeModelFacts(ctx context.Context, home, image string, record []byte, run RuntimeDocker) (baseline, live []byte, err error) {
	if image == "" || strings.HasPrefix(image, "-") {
		return nil, nil, fmt.Errorf("The product image is unavailable.")
	}
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return nil, nil, err
	}
	installation, err := agentProfileOperationLockContext(ctx, root, openCodeOperation+".worker")
	if err != nil {
		return nil, nil, fmt.Errorf("The OpenCode installation did not complete in time.")
	}
	defer agentProfileUnlock(installation)
	if download, err := readAgentProfileOperation(root, openCodeOperation); err != nil || download.Status != "succeeded" {
		return nil, nil, fmt.Errorf("The pinned OpenCode is not installed.")
	}
	volume, err := OpenCodeVolume(home)
	if err != nil {
		return nil, nil, err
	}
	archive, err := openCodeFactsArchive(record)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	marker := "culler-model-facts-" + hex.EncodeToString(nonce)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	output, err := run(ctx, archive, "run", "--rm", operatorconfig.PidsLimitFlag, "-i", "--pull", "never", "--platform", "linux/arm64", "--network", "none",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "10002:10002",
		"--tmpfs", "/tmp:rw,nosuid,nodev,mode=1777", "--tmpfs", "/etc/opencode:ro,nosuid,nodev,noexec,mode=0555",
		"--mount", "type=volume,src="+volume+",dst="+OpenCodeRoot+",readonly,volume-nocopy",
		image, "/usr/bin/env", "-i", "PATH=/usr/local/bin:/usr/bin:/bin", "/bin/sh", "-ec", openCodeFactsScript, "derive",
		opencodecatalog.Release().OpenCodeVersion, marker)
	parts := bytes.Split(output, []byte("\n"+marker+"\n"))
	if err != nil || len(parts) != 4 {
		cause := lastLine(parts[0])
		if len(cause) > 400 {
			cause = strings.ToValidUTF8(cause[:400], "")
		}
		if ctx.Err() != nil || cause == "" {
			cause = "The derivation exceeded its time limit."
		}
		return nil, nil, fmt.Errorf("The pinned OpenCode could not derive the model facts: %s", cause)
	}
	return parts[1], parts[2], nil
}
