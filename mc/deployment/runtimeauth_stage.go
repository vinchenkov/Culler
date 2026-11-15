package deployment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	operatorconfig "mc/config"
)

type RuntimeAuthSourceStage struct {
	Sources    RuntimeAuthSources
	sourceRoot string
	stageRoot  string
	disposable *runtimeAuthDisposableRoot
}

func (s *RuntimeAuthSourceStage) Cleanup() error {
	if s == nil || s.disposable == nil {
		return nil
	}
	err := s.disposable.Cleanup("runtime-auth source stage")
	s.disposable = nil
	if err == nil {
		s.stageRoot = ""
	}
	return err
}

func stageRuntimeAuthBody(
	stage *RuntimeAuthSourceStage, body []byte, name string,
) (string, error) {
	target := filepath.Join(stage.stageRoot, name)
	fd, err := unix.Openat(
		stage.disposable.rootFD, name,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), target)
	if err := unix.Fchmod(fd, 0o600); err != nil {
		file.Close()
		unix.Unlinkat(stage.disposable.rootFD, name, 0)
		return "", err
	}
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		unlinkErr := unix.Unlinkat(stage.disposable.rootFD, name, 0)
		return "", errors.Join(err, unlinkErr)
	}
	return target, nil
}

// StageRuntimeAuthSources copies the credentials of a Culler sign-in into a disposable folder.
func StageRuntimeAuthSources(
	home string,
	sources RuntimeAuthSources,
	acquisitions ...*RuntimeAuthAcquisition,
) (result *RuntimeAuthSourceStage, retErr error) {
	return stageRuntimeAuthSources(home, sources, acquisitions...)
}

func stageRuntimeAuthSources(
	home string,
	sources RuntimeAuthSources,
	acquisitions ...*RuntimeAuthAcquisition,
) (result *RuntimeAuthSourceStage, retErr error) {
	if len(acquisitions) > 1 {
		return nil, fmt.Errorf("at most one runtime-auth acquisition may be staged")
	}
	var acquisition *RuntimeAuthAcquisition
	if len(acquisitions) == 1 {
		acquisition = acquisitions[0]
	}
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime-auth source-stage MC_HOME: %w", err)
	}
	if err := requireOwnerOnlyDirectory(canonicalHome, "MC_HOME"); err != nil {
		return nil, err
	}
	catalog, err := LoadRuntimeCatalog(canonicalHome)
	if err != nil {
		return nil, err
	}
	bindings, err := validateRuntimeBindings(catalog, sources.Bindings)
	if err != nil {
		return nil, err
	}
	credentials, err := catalog.credentialsOf(bindings)
	if err != nil {
		return nil, err
	}
	if err := validateRuntimeAuthEnvironment(sources.Environment); err != nil {
		return nil, err
	}
	sourceRoot, err := prepareRuntimeAuthSourceRoot(canonicalHome)
	if err != nil {
		return nil, err
	}
	if err := requireOwnerOnlyDirectory(sourceRoot, "runtime-auth source root"); err != nil {
		return nil, err
	}
	if acquisition != nil {
		if acquisition.disposable == nil || acquisition.sourceRoot != sourceRoot {
			return nil, fmt.Errorf("runtime-auth acquisition is not bound to this MC_HOME source root")
		}
		if err := acquisition.disposable.requireLinkedWithoutReplacement(
			sourceRoot, "runtime-auth acquisition flow"); err != nil {
			return nil, err
		}
	}
	stageRoot, err := os.MkdirTemp(sourceRoot, ".provider-import-")
	if err != nil {
		return nil, fmt.Errorf("create runtime-auth source stage: %w", err)
	}
	disposable, err := openRuntimeAuthDisposableRoot(
		sourceRoot, stageRoot, ".provider-import-", "runtime-auth source stage")
	if err != nil {
		removeErr := os.Remove(stageRoot)
		return nil, errors.Join(err, removeErr)
	}
	stage := &RuntimeAuthSourceStage{
		Sources:    sources,
		sourceRoot: sourceRoot,
		stageRoot:  stageRoot,
		disposable: disposable,
	}
	success := false
	defer func() {
		if !success {
			if cleanupErr := stage.Cleanup(); cleanupErr != nil {
				if retErr != nil {
					retErr = fmt.Errorf("%v; staged-source cleanup also failed: %w", retErr, cleanupErr)
				} else {
					retErr = cleanupErr
				}
			}
		}
	}()
	stagedKinds := map[string]bool{}
	for _, credential := range credentials {
		kind := catalog.Credentials[credential].Kind
		if stagedKinds[kind] {
			continue
		}
		stagedKinds[kind] = true
		switch kind {
		case operatorconfig.CredentialKindCodexOAuth:
			if acquisition == nil || len(acquisition.codexAuthBody) == 0 {
				err = fmt.Errorf("a Codex subscription login requires a Culler sign-in")
			} else {
				stage.Sources.CodexAuthFile, err = stageRuntimeAuthBody(
					stage, acquisition.codexAuthBody, "codex-auth.json")
			}
		case operatorconfig.CredentialKindClaudeOAuth:
			if acquisition == nil || len(acquisition.claudeCredentialsBody) == 0 {
				err = fmt.Errorf("a Claude Code subscription login requires a Culler sign-in")
			} else {
				var subscription []byte
				subscription, err = claudeSubscriptionCredential(acquisition.claudeCredentialsBody)
				if err == nil {
					stage.Sources.ClaudeCredentialsFile, err = stageRuntimeAuthBody(
						stage, subscription, "claude-credentials.json")
					clear(subscription)
				}
			}
		default:
			err = fmt.Errorf("The connection type is unsupported.")
		}
		if err != nil {
			return nil, fmt.Errorf("stage credential %s source: %w", credential, err)
		}
	}
	if err := unix.Fsync(stage.disposable.rootFD); err != nil {
		return nil, fmt.Errorf("sync runtime-auth source stage: %w", err)
	}
	if err := unix.Fsync(stage.disposable.parentFD); err != nil {
		return nil, fmt.Errorf("sync runtime-auth source root: %w", err)
	}
	success = true
	return stage, nil
}

// ImportStagedRuntimeAuth snapshots the fixed source files through the stage's
// retained directory descriptor, then removes the entire source stage before
// building or publishing any grants. Renaming or replacing the textual stage
// path therefore cannot substitute credentials or survive a successful import.
func ImportStagedRuntimeAuth(
	home string,
	stage *RuntimeAuthSourceStage,
	verifier RuntimeAuthVerifier,
) (string, error) {
	return importStagedRuntimeAuth(home, stage, verifier, importPreparedRuntimeAuth)
}

type stagedRuntimeAuthPublisher func(string, RuntimeCatalog, []string, RuntimeAuthVerifier, func(string) (any, error)) (string, error)

func importStagedRuntimeAuth(home string, stage *RuntimeAuthSourceStage, verifier RuntimeAuthVerifier, publish stagedRuntimeAuthPublisher) (string, error) {
	if stage == nil || stage.disposable == nil {
		return "", fmt.Errorf("runtime-auth source stage is unavailable")
	}
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return "", fmt.Errorf("resolve staged runtime-auth MC_HOME: %w", err)
	}
	if err := requireOwnerOnlyDirectory(canonicalHome, "MC_HOME"); err != nil {
		return "", err
	}
	sourceRoot := runtimeAuthSourceRoot(canonicalHome)
	if stage.sourceRoot != sourceRoot {
		return "", fmt.Errorf("runtime-auth source stage belongs to another MC_HOME")
	}
	if err := stage.disposable.requireLinkedWithoutReplacement(
		sourceRoot, "runtime-auth source stage"); err != nil {
		return "", err
	}
	catalog, err := LoadRuntimeCatalog(canonicalHome)
	if err != nil {
		return "", err
	}
	bindings, err := validateRuntimeBindings(catalog, stage.Sources.Bindings)
	if err != nil {
		return "", err
	}
	credentials, err := catalog.credentialsOf(bindings)
	if err != nil {
		return "", err
	}
	if err := validateRuntimeAuthEnvironment(stage.Sources.Environment); err != nil {
		return "", err
	}
	if verifier == nil {
		return "", fmt.Errorf("runtime-auth live verifier is unavailable")
	}
	bodies := make(map[string][]byte, len(credentials))
	defer func() {
		for _, body := range bodies {
			clear(body)
		}
	}()
	for _, credential := range credentials {
		switch catalog.Credentials[credential].Kind {
		case operatorconfig.CredentialKindCodexOAuth:
			bodies[credential], err = readRuntimeAuthSecretAt(
				stage.disposable.rootFD, nil, "codex-auth.json", "staged Codex auth")
		case operatorconfig.CredentialKindClaudeOAuth:
			bodies[credential], err = readRuntimeAuthSecretAt(
				stage.disposable.rootFD, nil,
				"claude-credentials.json", "staged Claude credentials")
		default:
			err = fmt.Errorf("The connection type is unsupported.")
		}
		if err != nil {
			return "", fmt.Errorf("read staged credential %s: %w", credential, err)
		}
	}
	if err := stage.disposable.requireLinkedWithoutReplacement(
		sourceRoot, "runtime-auth source stage"); err != nil {
		return "", err
	}
	if err := stage.Cleanup(); err != nil {
		return "", fmt.Errorf("remove runtime-auth source stage before import: %w", err)
	}
	return publish(
		canonicalHome, catalog, bindings, verifier,
		func(credential string) (any, error) {
			return buildRuntimeGrantBody(credential, catalog.Credentials[credential], bodies[credential])
		})
}
