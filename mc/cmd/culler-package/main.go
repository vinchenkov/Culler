package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"mc/deployment"
	"mc/substrate"
)

func main() {
	root := flag.String("root", "", "The source root.")
	binary := flag.String("binary", "", "The production binary.")
	agentsView := flag.String("agentsview-archive", "", "The pinned AgentsView release archive.")
	commit := flag.String("commit", "", "The release commit.")
	output := flag.String("output", "", "The package path.")
	verify := flag.String("verify-binary", "", "The binary to verify without a package.")
	platform := flag.String("platform", "darwin-arm64", "The required binary platform.")
	flag.Parse()
	if !regexp.MustCompile(`^[0-9a-f]{40,64}$`).MatchString(*commit) {
		fail(errors.New("the release requires a full commit identifier"))
	}
	if *verify != "" {
		failIf(validateBinary(*verify, *commit, *platform))
		return
	}
	if *root == "" || *binary == "" || *agentsView == "" || *output == "" || flag.NArg() != 0 {
		fail(errors.New("the source root, binary, AgentsView release archive, commit, and output are required"))
	}
	failIf(buildPackage(*root, *binary, *agentsView, *commit, *output))
}

func buildPackage(root, binary, agentsViewArchive, commit, output string) error {
	if err := validateBinary(binary, commit, "darwin-arm64"); err != nil {
		return err
	}
	runtimes, err := runtimePins(root)
	if err != nil {
		return err
	}
	pin, err := deployment.LoadAgentsViewPin()
	if err != nil {
		return err
	}
	hostBun, err := deployment.LoadHostBunPin()
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "culler-agentsview-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	executable, err := deployment.ExtractAgentsViewExecutable(agentsViewArchive, pin, scratch)
	if err != nil {
		return err
	}
	if err := writePackage(root, binary, executable, output); err != nil {
		return err
	}
	metadata, err := packageMetadata(output, commit, runtimes, pin, hostBun)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(metadata)
}

// packageMetadata is the build record that publication turns into the
// release record.
func packageMetadata(output, commit string, runtimes map[string]string, pin deployment.AgentsViewPin, hostBun deployment.HostBunPin) (map[string]any, error) {
	file, err := os.Open(output)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"commit": commit,
		"name":   filepath.Base(output), "size": size, "sha256": hex.EncodeToString(hash.Sum(nil)),
		"schema": substrate.CurrentSchemaID, "runtimes": runtimes,
		"agentsview": map[string]string{
			"version": pin.Version, "archive_sha256": pin.Archive.SHA256, "executable_sha256": pin.Executable.SHA256,
		},
		"host_bun": hostBun,
	}, nil
}

func runtimePins(root string) (map[string]string, error) {
	var config struct {
		Tools map[string]string `toml:"tools"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, ".mise.toml"), &config); err != nil {
		return nil, fmt.Errorf("read the runtime pins: %w", err)
	}
	if "go"+config.Tools["go"] != runtime.Version() || config.Tools["bun"] == "" {
		return nil, errors.New("the runtime pins differ from the package toolchain")
	}
	return map[string]string{"go": config.Tools["go"], "bun": config.Tools["bun"]}, nil
}

func failIf(err error) {
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func validateBinary(path, commit, platform string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the binary build record: %w", err)
	}
	if err := validateBuildInfo(info, commit, platform); err != nil {
		return err
	}
	if err := validateDebugData(path, platform); err != nil {
		return err
	}
	identity, err := binaryReleaseID(path, platform)
	if err != nil {
		return err
	}
	if identity != commit {
		return errors.New("the binary contains a different release identity")
	}
	return nil
}

// validateDebugData rejects a binary that holds DWARF debug data. A release
// build links with -w, which omits that data and keeps the symbol table.
func validateDebugData(path, platform string) error {
	switch platform {
	case "darwin-arm64":
		file, err := macho.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		if file.Segment("__DWARF") != nil {
			return errors.New("the binary contains DWARF debug data")
		}
	case "linux-arm64":
		file, err := elf.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		if file.Section(".debug_info") != nil || file.Section(".zdebug_info") != nil {
			return errors.New("the binary contains DWARF debug data")
		}
	default:
		return errors.New("the binary platform is unsupported")
	}
	return nil
}

func binaryReleaseID(path, platform string) (string, error) {
	var address uint64
	var order binary.ByteOrder
	var read func(uint64, uint64) ([]byte, error)
	if platform == "darwin-arm64" {
		file, err := macho.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		if file.Symtab == nil {
			return "", errors.New("the binary has no symbol table")
		}
		for _, symbol := range file.Symtab.Syms {
			if symbol.Name == "main.releaseBuildID" {
				address = symbol.Value
			}
		}
		order = file.ByteOrder
		read = func(address, size uint64) ([]byte, error) {
			for _, section := range file.Sections {
				if address >= section.Addr && address-section.Addr <= section.Size && size <= section.Size-(address-section.Addr) {
					data, err := section.Data()
					if err != nil {
						return nil, err
					}
					return data[address-section.Addr : address-section.Addr+size], nil
				}
			}
			return nil, errors.New("the release identity has an invalid address")
		}
	} else if platform == "linux-arm64" {
		file, err := elf.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		symbols, err := file.Symbols()
		if err != nil {
			return "", err
		}
		for _, symbol := range symbols {
			if symbol.Name == "main.releaseBuildID" {
				address = symbol.Value
			}
		}
		order = file.ByteOrder
		read = func(address, size uint64) ([]byte, error) {
			for _, section := range file.Sections {
				if address >= section.Addr && address-section.Addr <= section.Size && size <= section.Size-(address-section.Addr) {
					data, err := section.Data()
					if err != nil {
						return nil, err
					}
					return data[address-section.Addr : address-section.Addr+size], nil
				}
			}
			return nil, errors.New("the release identity has an invalid address")
		}
	} else {
		return "", errors.New("the binary platform is unsupported")
	}
	if address == 0 {
		return "", errors.New("the binary has no release identity")
	}
	header, err := read(address, 16)
	if err != nil {
		return "", err
	}
	length := order.Uint64(header[8:])
	if length != 40 && length != 64 {
		return "", errors.New("the binary release identity has an invalid length")
	}
	value, err := read(order.Uint64(header[:8]), length)
	return string(value), err
}

func validateBuildInfo(info *debug.BuildInfo, commit, platform string) error {
	if info.GoVersion != runtime.Version() {
		return errors.New("the binary Go version differs from the package toolchain")
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"]+"-"+settings["GOARCH"] != platform {
		return errors.New("the binary platform differs from the release platform")
	}
	if settings["-tags"] != "" {
		return errors.New("the production binary contains custom build tags")
	}
	if settings["CGO_ENABLED"] != "0" {
		return errors.New("the production binary requires a static Go build")
	}
	// A -trimpath build records no -ldflags setting. The binary itself then
	// shows the release identity and the absence of DWARF data.
	fields := strings.Fields(settings["-ldflags"])
	want := "main.releaseBuildID=" + commit
	found, omitsDWARF := false, false
	for i, field := range fields {
		if field == "-w" {
			omitsDWARF = true
		}
		if field == "-X" && i+1 < len(fields) && fields[i+1] == want {
			found = true
		}
	}
	if (settings["-ldflags"] != "" && !found) || info.Path != "mc/cmd/mc" {
		return errors.New("the binary does not identify the release commit")
	}
	if settings["-ldflags"] != "" && !omitsDWARF {
		return errors.New("the binary build record keeps DWARF debug data")
	}
	return nil
}

func packageFiles(root, binary, agentsView string) map[string]string {
	files := map[string]string{
		"bin/mc": binary, "bin/install.sh": filepath.Join(root, "install.sh"), "bin/agentsview": agentsView,
	}
	for _, name := range deployment.HostReleaseFiles() {
		files["host/"+name] = filepath.Join(root, filepath.FromSlash(name))
	}
	for _, name := range deployment.RunnerReleaseFiles() {
		files["runner/"+name] = filepath.Join(root, "runner", filepath.FromSlash(name))
	}
	return files
}

func writePackage(root, binary, agentsView, output string) (result error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	inputs := []*string{&binary, &agentsView}
	for _, input := range inputs {
		parent, err := filepath.EvalSymlinks(filepath.Dir(*input))
		if err != nil {
			return err
		}
		*input = filepath.Join(parent, filepath.Base(*input))
	}
	files := packageFiles(root, binary, agentsView)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if result != nil {
			_ = os.Remove(output)
		}
	}()
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	for _, name := range names {
		if err := appendFile(archive, name, files[name]); err != nil {
			_ = archive.Close()
			_ = compressed.Close()
			_ = file.Close()
			return err
		}
	}
	return errors.Join(archive.Close(), compressed.Close(), file.Close())
}

func appendFile(archive *tar.Writer, name, source string) error {
	for path := source; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("the package input contains a symbolic link: %s", name)
		}
		if filepath.Dir(path) == path || path == "." {
			break
		}
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return fmt.Errorf("the package input is not a regular nonempty file: %s", name)
	}
	mode := int64(0o600)
	if strings.HasPrefix(name, "bin/") {
		mode = 0o700
	}
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: info.Size(), ModTime: time.Unix(0, 0)}); err != nil {
		return err
	}
	_, err = io.Copy(archive, file)
	return err
}
