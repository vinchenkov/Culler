package deployment

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path"
	"regexp"
	"strings"
)

var immutableProjectImage = regexp.MustCompile(`^(?:[a-zA-Z0-9._:/-]+@)?sha256:[0-9a-f]{64}$`)

type projectImageFile struct {
	Kind   byte
	Mode   int64
	UID    int
	GID    int
	Link   string
	Digest string
	Body   []byte
}

// CheckProjectImage inspects container archives without executing image programs.
func CheckProjectImage(ctx context.Context, base, image string) error {
	if !immutableProjectImage.MatchString(base) || !immutableProjectImage.MatchString(image) {
		return fmt.Errorf("the Project image check requires immutable image digests")
	}
	baseFiles, err := exportProjectImage(ctx, base)
	if err != nil {
		return fmt.Errorf("inspect the selected Project base: %w", err)
	}
	files := baseFiles
	if image != base {
		files, err = exportProjectImage(ctx, image)
		if err != nil {
			return fmt.Errorf("inspect the candidate Project image: %w", err)
		}
	}
	return compareProjectPlatform(baseFiles, files)
}

func exportProjectImage(ctx context.Context, image string) (map[string]projectImageFile, error) {
	body, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{json .}}", image).Output()
	if err != nil {
		return nil, err
	}
	var config struct {
		OS           string `json:"Os"`
		Architecture string
		Config       struct {
			Volumes map[string]any
			Env     []string
		}
	}
	if json.Unmarshal(body, &config) != nil || config.OS != "linux" || config.Architecture != "arm64" || len(config.Config.Volumes) != 0 {
		return nil, fmt.Errorf("the Project image requires Linux arm64 and no declared Docker volumes")
	}
	for _, value := range config.Config.Env {
		key, value, _ := strings.Cut(value, "=")
		if value != "" && (strings.HasPrefix(key, "LD_") || key == "NODE_OPTIONS" || key == "BUN_OPTIONS" || key == "GCONV_PATH" || key == "LOCPATH" || key == "GLIBC_TUNABLES") {
			return nil, fmt.Errorf("the Project image contains a reserved runtime variable: %s", key)
		}
	}
	body, err = exec.CommandContext(ctx, "docker", "create", "--network", "none", "--entrypoint", "/bin/true", image).Output()
	if err != nil {
		return nil, err
	}
	container := strings.TrimSpace(string(body))
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(container) {
		return nil, fmt.Errorf("Docker returned an invalid container identity")
	}
	defer exec.Command("docker", "rm", "-f", container).Run()
	command := exec.CommandContext(ctx, "docker", "cp", container+":/.", "-")
	reader, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	files, readErr := readProjectImage(reader)
	if readErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return files, nil
}

func protectedProjectLibrary(name string) bool {
	for _, prefix := range []string{"lib/", "lib64/", "usr/lib/", "usr/lib64/", "usr/local/lib/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func readProjectImage(input io.Reader) (map[string]projectImageFile, error) {
	reader := tar.NewReader(input)
	files := map[string]projectImageFile{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(strings.TrimSuffix(header.Name, "/"), "./")
		if name == "" {
			name = "."
		}
		if name != path.Clean(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("the image contains an invalid path")
		}
		if _, exists := files[name]; exists {
			return nil, fmt.Errorf("the image repeats path %s", name)
		}
		if header.Typeflag != tar.TypeDir && header.Mode&06000 != 0 {
			return nil, fmt.Errorf("the Project image contains a setuid or setgid file: %s", name)
		}
		for key := range header.PAXRecords {
			if strings.Contains(strings.ToLower(key), "security.capability") {
				return nil, fmt.Errorf("the Project image contains file capabilities: %s", name)
			}
		}
		for key := range header.Xattrs {
			if strings.EqualFold(key, "security.capability") {
				return nil, fmt.Errorf("the Project image contains file capabilities: %s", name)
			}
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeSymlink && header.Typeflag != tar.TypeLink {
			return nil, fmt.Errorf("the Project image contains an unsupported file type: %s", name)
		}
		file := projectImageFile{Kind: header.Typeflag, Mode: header.Mode, UID: header.Uid, GID: header.Gid, Link: header.Linkname}
		retain := name == "etc/passwd" || name == "etc/group" || name == "etc/ld.so.cache"
		if retain && header.Size > 4<<20 {
			return nil, fmt.Errorf("the Project platform file exceeds its limit: %s", name)
		}
		if header.Typeflag == tar.TypeReg && (protectedProjectLibrary(name) || strings.HasPrefix(name, "etc/") || retain) {
			hash := sha256.New()
			if retain {
				file.Body, err = io.ReadAll(io.TeeReader(reader, hash))
			} else {
				_, err = io.Copy(hash, reader)
			}
			if err != nil {
				return nil, err
			}
			file.Digest = hex.EncodeToString(hash.Sum(nil))
		}
		files[name] = file
	}
}

func sameProjectFile(a, b projectImageFile) bool {
	return a.Kind == b.Kind && a.Mode == b.Mode && a.UID == b.UID && a.GID == b.GID && a.Link == b.Link && a.Digest == b.Digest
}

func compareProjectPlatform(base, candidate map[string]projectImageFile) error {
	reserved := []string{".", "etc", "usr", "usr/local", "home", "opt", "mc", "mc/spine", "mc/private", "mc/private/attachments", "mc/private/attachments/out", "mc/attachments", "mc/attachments/in", "mc/codex", "home/mc-model", "workspace", "cache"}
	for _, name := range reserved {
		before, ok := base[name]
		after, found := candidate[name]
		if !ok || !found || before.Kind != tar.TypeDir || !sameProjectFile(before, after) {
			return fmt.Errorf("the Project image changes a required directory: %s", name)
		}
	}
	for name, before := range base {
		protected := strings.HasPrefix(name, "mc/") || protectedProjectLibrary(name) || name == "lib" || name == "lib64" || name == "usr" || name == "usr/lib" || name == "usr/lib64" || name == "usr/local/lib" || name == "etc/nsswitch.conf" || name == "etc/ld.so.conf" || strings.HasPrefix(name, "etc/ld.so.conf.d")
		if protected {
			after, ok := candidate[name]
			if !ok || !sameProjectFile(before, after) {
				return fmt.Errorf("the Project image changes a required platform file: %s", name)
			}
		}
	}
	libraryNames := map[string]bool{}
	for name, file := range base {
		if protectedProjectLibrary(name) && file.Kind != tar.TypeDir {
			libraryNames[path.Base(name)] = true
		}
	}
	for name, file := range candidate {
		if strings.HasPrefix(name, "mc/") || name == "workspace/source" || strings.HasPrefix(name, "workspace/source/") {
			before, exists := base[name]
			if !exists || !sameProjectFile(before, file) {
				return fmt.Errorf("the Project image changes a reserved mount path: %s", name)
			}
		}
		if _, exists := base[name]; !exists && protectedProjectLibrary(name) && file.Kind != tar.TypeDir && libraryNames[path.Base(name)] {
			return fmt.Errorf("the Project image shadows a required library: %s", name)
		}
		if name == "etc/ld.so.preload" || strings.HasPrefix(name, "opt/culler-runtime/") || name == "opt/culler-runtime" ||
			strings.HasPrefix(name, "etc/ld.so.conf.d/") && base[name].Kind == 0 {
			return fmt.Errorf("the Project image changes a reserved runtime path: %s", name)
		}
	}
	if err := checkProjectAccounts(base["etc/passwd"], candidate["etc/passwd"], false); err != nil {
		return err
	}
	if err := checkProjectAccounts(base["etc/group"], candidate["etc/group"], true); err != nil {
		return err
	}
	return checkProjectLoaderCache(base["etc/ld.so.cache"], candidate["etc/ld.so.cache"])
}

func checkProjectAccounts(base, candidate projectImageFile, group bool) error {
	if base.Kind != tar.TypeReg || candidate.Kind != tar.TypeReg || base.UID != candidate.UID || base.GID != candidate.GID || base.Mode != candidate.Mode {
		return fmt.Errorf("the Project image changes account file permissions")
	}
	required := map[string]string{}
	for _, line := range strings.Split(string(base.Body), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 3 && (fields[2] == "0" || fields[2] == "10001" || fields[2] == "10002") {
			required[fields[0]] = line
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(candidate.Body), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 4 || seen[fields[0]] {
			return fmt.Errorf("the Project image contains invalid account records")
		}
		seen[fields[0]] = true
		prior, reserved := required[fields[0]]
		if reserved && prior != line || !reserved && (fields[2] == "0" || fields[2] == "10001" || fields[2] == "10002") || !group && !reserved && (fields[3] == "10001" || fields[3] == "10002") {
			return fmt.Errorf("the Project image changes a required account: %s", fields[0])
		}
	}
	for name := range required {
		if !seen[name] {
			return fmt.Errorf("the Project image omits a required account: %s", name)
		}
	}
	return nil
}

func projectLoaderCache(body []byte) (map[string]map[string]bool, error) {
	if len(body) < 48 || !bytes.Equal(body[:20], []byte("glibc-ld.so.cache1.1")) {
		return nil, fmt.Errorf("the Project image has an unsupported library cache")
	}
	count := uint64(binary.LittleEndian.Uint32(body[20:24]))
	if count > 100000 || 48+count*24 > uint64(len(body)) {
		return nil, fmt.Errorf("the Project image has an invalid library cache")
	}
	read := func(offset uint32) (string, error) {
		if uint64(offset) < 48+count*24 || uint64(offset) >= uint64(len(body)) {
			return "", fmt.Errorf("the library cache contains an invalid path")
		}
		rest := body[offset:]
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return "", fmt.Errorf("the library cache contains an incomplete path")
		}
		return string(rest[:end]), nil
	}
	entries := map[string]map[string]bool{}
	for i := uint64(0); i < count; i++ {
		offset := 48 + i*24
		name, err := read(binary.LittleEndian.Uint32(body[offset+4 : offset+8]))
		if err != nil {
			return nil, err
		}
		file, err := read(binary.LittleEndian.Uint32(body[offset+8 : offset+12]))
		if err != nil {
			return nil, err
		}
		if entries[name] == nil {
			entries[name] = map[string]bool{}
		}
		flags := binary.LittleEndian.Uint32(body[offset : offset+4])
		osVersion := binary.LittleEndian.Uint32(body[offset+12 : offset+16])
		hwcap := binary.LittleEndian.Uint64(body[offset+16 : offset+24])
		if hwcap != 0 {
			return nil, fmt.Errorf("the Project image contains an unsupported hardware library selection")
		}
		entries[name][fmt.Sprintf("%08x:%08x:%s", flags, osVersion, file)] = true
	}
	return entries, nil
}

func checkProjectLoaderCache(base, candidate projectImageFile) error {
	if base.Kind != tar.TypeReg || candidate.Kind != tar.TypeReg || base.UID != candidate.UID || base.GID != candidate.GID || base.Mode != candidate.Mode {
		return fmt.Errorf("the Project image changes library cache permissions")
	}
	before, err := projectLoaderCache(base.Body)
	if err != nil {
		return err
	}
	after, err := projectLoaderCache(candidate.Body)
	if err != nil {
		return err
	}
	for name, paths := range before {
		if len(paths) != len(after[name]) {
			return fmt.Errorf("the Project image redirects a required library: %s", name)
		}
		for file := range paths {
			if !after[name][file] {
				return fmt.Errorf("the Project image redirects a required library: %s", name)
			}
		}
	}
	return nil
}
