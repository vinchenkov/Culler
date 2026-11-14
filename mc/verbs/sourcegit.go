package verbs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// The helpers in this file read a Project repository with an isolated Git
// configuration. The seeding projection, the onboarding, and the uninstall
// use them. The git binary is a package variable so that a test can inject
// a fake; production uses the pinned git of the image.
var sourceGitBin = "git"

var canonicalHex = regexp.MustCompile(`^[0-9a-f]+$`)

// oidLen is the hex length of an object name in the given format.
func oidLen(objectFormat string) int {
	if objectFormat == "sha256" {
		return 64
	}
	return 40
}

func validateSetupObjectFormat(objectFormat string) error {
	if objectFormat != "sha1" && objectFormat != "sha256" {
		return Domainf("object format %q is outside the closed set", objectFormat)
	}
	return nil
}

// gitOutput runs git with an explicit environment and optional stdin, and
// returns the captured stdout. A non-zero exit is an error that carries stderr.
func gitOutput(dir string, env []string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command(sourceGitBin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}

// sourceGitEnv is the environment for a read of the real source repository.
// Git reads the config of the repository itself, but not the global or the
// system config of the operator.
func sourceGitEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"HOME=" + os.TempDir(),
		"GIT_TERMINAL_PROMPT=0",
	}
}

func gitAtSource(dir string, args ...string) ([]byte, error) {
	return gitOutput(dir, sourceGitEnv(), nil, args...)
}

func sourceCommonDirAbs(sourceRepo string) (string, error) {
	out, err := gitAtSource(sourceRepo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", Domainf("could not resolve the source git common dir: %v", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fileNonEmpty(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// checkSourceExtractable refuses a source whose object graph a plain walk
// cannot reproduce: alternates, grafts, and replace refs rewrite the
// reachable set, and a shallow or partial source has missing objects.
func checkSourceExtractable(sourceRepo string) error {
	gcd, err := sourceCommonDirAbs(sourceRepo)
	if err != nil {
		return err
	}
	if fileNonEmpty(filepath.Join(gcd, "objects", "info", "alternates")) {
		return Domainf("the source has object alternates")
	}
	if fileNonEmpty(filepath.Join(gcd, "info", "grafts")) {
		return Domainf("the source has commit grafts")
	}
	if rep, err := gitAtSource(sourceRepo, "for-each-ref", "--format=%(refname)", "refs/replace/"); err == nil &&
		len(strings.TrimSpace(string(rep))) > 0 {
		return Domainf("the source has replace refs")
	}
	if sh, err := gitAtSource(sourceRepo, "rev-parse", "--is-shallow-repository"); err == nil &&
		strings.TrimSpace(string(sh)) == "true" {
		return Domainf("the source is a shallow repository")
	}
	if _, err := gitAtSource(sourceRepo, "config", "--get", "extensions.partialClone"); err == nil {
		return Domainf("the source is a partial clone")
	}
	if promisor, err := gitAtSource(sourceRepo, "config", "--get-regexp", `^remote\..*\.promisor$`); err == nil &&
		len(strings.TrimSpace(string(promisor))) > 0 {
		return Domainf("the source has a promisor remote")
	}
	if hits, _ := filepath.Glob(filepath.Join(gcd, "objects", "pack", "*.promisor")); len(hits) > 0 {
		return Domainf("the source has promisor packs")
	}
	return nil
}

// syntheticGitContext creates a config-free and ref-free git dir whose only
// setting is the object format. It returns the dir and the environment that
// reads the source object directory through it, with replace objects off and
// the git config of the operator isolated. The caller removes the dir.
func syntheticGitContext(objectFormat, sourceObjectDir string) (string, []string, error) {
	syn, err := os.MkdirTemp("", "mc-setup-syn-")
	if err != nil {
		return "", nil, Domainf("could not create a synthetic git context: %v", err)
	}
	for _, d := range []string{"objects", "refs"} {
		if err := os.Mkdir(filepath.Join(syn, d), 0o700); err != nil {
			os.RemoveAll(syn)
			return "", nil, Domainf("could not create a synthetic git context: %v", err)
		}
	}
	config := "[core]\n\trepositoryformatversion = 1\n"
	if objectFormat == "sha256" {
		config += "[extensions]\n\tobjectFormat = sha256\n"
	}
	if err := os.WriteFile(filepath.Join(syn, "config"), []byte(config), 0o600); err != nil {
		os.RemoveAll(syn)
		return "", nil, Domainf("could not write the synthetic git context: %v", err)
	}
	if err := os.WriteFile(filepath.Join(syn, "HEAD"), []byte("ref: refs/heads/none\n"), 0o600); err != nil {
		os.RemoveAll(syn)
		return "", nil, Domainf("could not write the synthetic git context: %v", err)
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_DIR=" + syn,
		"GIT_OBJECT_DIRECTORY=" + sourceObjectDir,
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"HOME=" + syn,
		"GIT_TERMINAL_PROMPT=0",
	}
	return syn, env, nil
}

func resolveBaseOID(sourceRepo, targetRef, objectFormat string) (string, error) {
	if targetRef == "" {
		return "", Domainf("no target ref to resolve")
	}
	out, err := gitAtSource(sourceRepo, "rev-parse", "--verify", "--end-of-options", targetRef+"^{commit}")
	if err != nil {
		return "", Domainf("could not resolve target ref %q: %v", targetRef, err)
	}
	oid := strings.TrimSpace(string(out))
	if len(oid) != oidLen(objectFormat) || !canonicalHex.MatchString(oid) {
		return "", Domainf("resolved a non-canonical base OID")
	}
	return oid, nil
}

// rejectReservedTreeComponent refuses a base tree whose top level holds a
// name that Culler reserves: the Git cover and the artifact root.
func rejectReservedTreeComponent(sourceRepo, baseSHA string) error {
	out, err := gitAtSource(sourceRepo, "ls-tree", "--name-only", baseSHA)
	if err != nil {
		return Domainf("could not read the base tree: %v", err)
	}
	for _, name := range strings.Split(string(out), "\n") {
		switch strings.TrimSpace(name) {
		case ".git", projectArtifactDir:
			return Domainf("the base tree has a reserved %q root component", strings.TrimSpace(name))
		}
	}
	return nil
}
