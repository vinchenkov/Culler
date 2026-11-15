package verbs

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const SeedingProjectionOperation = "seeding-committed-tree"

// The two kinds of a Git seeding projection, as the mount plan names them.
const (
	seedingSourceGitCoverKind      = "seeding-source-git-cover"
	seedingCommittedProjectionKind = "seeding-committed-projection"
)

type SeedingProjectionEnvelope struct {
	ObjectFormat   string `json:"object_format"`
	Operation      string `json:"operation"`
	ProjectionRoot string `json:"projection_root"`
	SchemaVersion  int    `json:"schema_version"`
	SourceRoot     string `json:"source_root"`
	TargetRef      string `json:"target_ref"`
}

func ReadSeedingProjectionEnvelope(path string) (SeedingProjectionEnvelope, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return SeedingProjectionEnvelope{}, Domainf("seeding projection envelope is unreadable: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var env SeedingProjectionEnvelope
	if err := dec.Decode(&env); err != nil {
		return SeedingProjectionEnvelope{}, Domainf("seeding projection envelope is malformed: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return SeedingProjectionEnvelope{}, Domainf("seeding projection envelope carries trailing data")
	}
	return env, nil
}

func RunSeedingProjectionSetup(env SeedingProjectionEnvelope) (string, error) {
	if env.SchemaVersion != 1 || env.Operation != SeedingProjectionOperation ||
		env.SourceRoot != "/repo/source" || env.ProjectionRoot != "/repo/projection" ||
		!validStructuralText(env.TargetRef, maxPrivateScalarBytes) || env.TargetRef == "" {
		return "", Domainf("seeding projection envelope is outside its closed operation and path shape")
	}
	return MaterializeSeedingCommittedSource(env.SourceRoot, env.ProjectionRoot, env.TargetRef, env.ObjectFormat)
}

// MaterializeSeedingCommittedSource writes exactly one committed Git tree into
// an otherwise-empty run-scoped projection. It never copies the live checkout,
// so dirty, staged and untracked bytes cannot enter a Strategist proposal.
func MaterializeSeedingCommittedSource(sourceRoot, projectionRoot, targetRef, objectFormat string) (string, error) {
	if err := validateSetupObjectFormat(objectFormat); err != nil {
		return "", err
	}
	for _, path := range []string{sourceRoot, projectionRoot} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", Domainf("seeding committed projection path is not a non-symlink directory")
		}
	}
	entries, err := os.ReadDir(projectionRoot)
	if err != nil || len(entries) != 0 {
		return "", Domainf("seeding committed projection root is not empty")
	}
	commit, err := resolveBaseOID(sourceRoot, targetRef, objectFormat)
	if err != nil {
		return "", err
	}
	if err := checkSourceExtractable(sourceRoot); err != nil {
		return "", err
	}
	if err := rejectReservedTreeComponent(sourceRoot, commit); err != nil {
		return "", err
	}
	if err := extractCommittedArchive(sourceRoot, projectionRoot, commit, objectFormat); err != nil {
		return "", err
	}
	if err := os.Mkdir(filepath.Join(projectionRoot, ".git"), 0o555); err != nil {
		return "", Domainf("seeding committed projection could not create its inert Git cover: %v", err)
	}
	return commit, nil
}

func extractCommittedArchive(sourceRoot, projectionRoot, commit, objectFormat string) error {
	common, err := sourceCommonDirAbs(sourceRoot)
	if err != nil {
		return err
	}
	synthetic, env, err := syntheticGitContext(objectFormat, filepath.Join(common, "objects"))
	if err != nil {
		return err
	}
	defer os.RemoveAll(synthetic)
	cmd := exec.Command(sourceGitBin, "archive", "--format=tar", "--", commit)
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Domainf("seeding committed projection could not open its archive: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Domainf("seeding committed projection could not start its archive: %v", err)
	}
	extractErr := extractCommittedTar(projectionRoot, tar.NewReader(stdout))
	waitErr := cmd.Wait()
	if extractErr != nil {
		return extractErr
	}
	if waitErr != nil {
		return Domainf("seeding committed projection archive failed: %v: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func extractCommittedTar(root string, tr *tar.Reader) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return Domainf("seeding committed projection archive is unreadable: %v", err)
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return Domainf("seeding committed projection archive path %q escapes its root", hdr.Name)
		}
		dest := filepath.Join(root, name)
		if dest != filepath.Clean(dest) || !strings.HasPrefix(dest, root+string(filepath.Separator)) {
			return Domainf("seeding committed projection archive path %q is not confined", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return Domainf("seeding committed projection could not create directory: %v", err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return Domainf("seeding committed projection could not create parent: %v", err)
			}
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return Domainf("seeding committed projection could not create file: %v", err)
			}
			_, copyErr := io.Copy(file, tr)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				return Domainf("seeding committed projection could not write file: %v", firstError(copyErr, closeErr))
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return Domainf("seeding committed projection could not create symlink parent: %v", err)
			}
			if err := os.Symlink(hdr.Linkname, dest); err != nil {
				return Domainf("seeding committed projection could not create symlink: %v", err)
			}
		default:
			return Domainf("seeding committed projection archive contains unsupported entry type %d", hdr.Typeflag)
		}
	}
}

func firstError(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
