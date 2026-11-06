package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"mc/boundary"
	"mc/verbs"
)

// attachmentCanaryImage replaces the agent image of the deployment in the Docker test lanes.
var attachmentCanaryImage string

const attachmentCanaryOutputLimit = 64 * 1024

var attachmentCanaryContainerID = regexp.MustCompile(`^[0-9a-f]{64}$`)

type attachmentCanaryActive struct {
	Version       int    `json:"version"`
	Generation    string `json:"generation"`
	ContainerName string `json:"container_name"`
}

type attachmentCanaryInspect struct {
	ID     string `json:"Id"`
	Config struct {
		User   string            `json:"User"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode string   `json:"NetworkMode"`
		CapDrop     []string `json:"CapDrop"`
		SecurityOpt []string `json:"SecurityOpt"`
	} `json:"HostConfig"`
	Mounts []attachmentCanaryMount `json:"Mounts"`
}

type attachmentCanaryMount struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

// Docker Desktop reports a bind's Source two ways for the same directory: the
// host path we passed to --mount, or that path under /host_mnt, the VM's own
// mount point for shared host storage. Which one comes back is not stable —
// observed to differ per mount within one deployment, and between two
// inspections of one container — so a raw string comparison refuses a correct
// container. That killed the Collie tier outright on the live deployment
// (resident/src/collie-envelope.ts carries the same collapse and the full
// account); this is the second call site of the same class, and it gates
// supervision activation, so the failure here is a deployment that cannot be
// activated at all.
//
// Nothing is widened past the prefix: the remainder must begin at a separator
// and must then match exactly.
func normalizeDockerBindSource(source string) string {
	const vmPrefix = "/host_mnt"
	if remainder := strings.TrimPrefix(source, vmPrefix); remainder != source &&
		strings.HasPrefix(remainder, "/") {
		return remainder
	}
	return source
}

type attachmentCanaryAttempt struct {
	root, activePath, generation, containerName string
}

type attachmentCanaryGateReport struct {
	IsDirectory bool   `json:"is_directory"`
	UID         int    `json:"uid"`
	GID         int    `json:"gid"`
	Mode        int    `json:"mode"`
	Traverse    string `json:"traverse"`
}

type attachmentCanaryHostEvidence struct {
	device uint64
	inode  uint64
	size   int64
}

func runAttachmentCanary(home string) (out verbs.AttachmentCanaryReceipt, retErr error) {
	image := attachmentCanaryImage
	if image == "" {
		configured, err := configuredProductionImage()
		if err != nil {
			return out, err
		}
		image = configured
	}
	root := filepath.Join(home, "health", "attachment-canary")
	if err := trustAttachmentCanaryDirs(home); err != nil {
		return out, err
	}
	if err := ensureAttachmentCanaryDir(filepath.Join(home, "health")); err != nil {
		return out, err
	}
	if err := ensureAttachmentCanaryDir(root); err != nil {
		return out, err
	}
	if err := trustAttachmentCanaryDirs(filepath.Join(home, "health"), root); err != nil {
		return out, err
	}
	lock, err := lockAttachmentCanary(root)
	if err != nil {
		return out, err
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()
	if err := recoverAttachmentCanary(root); err != nil {
		return out, err
	}

	generationBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, generationBytes); err != nil {
		return out, fmt.Errorf("mint attachment canary generation: %w", err)
	}
	generation := hex.EncodeToString(generationBytes)
	attempt := attachmentCanaryAttempt{
		root:          filepath.Join(root, "attempt-"+generation),
		activePath:    filepath.Join(root, "active.json"),
		generation:    generation,
		containerName: "mc-attachment-canary-" + generation,
	}
	attemptLive := true
	defer func() {
		if attemptLive {
			retErr = errors.Join(retErr, cleanupAttachmentCanaryAttempt(attempt))
		}
	}()
	active := attachmentCanaryActive{
		Version: 1, Generation: generation, ContainerName: attempt.containerName,
	}
	if err := writeAttachmentCanaryAtomic(root, "active.json", 0o600, active); err != nil {
		return out, err
	}
	if err := os.Mkdir(attempt.root, 0o700); err != nil {
		return out, fmt.Errorf("create attachment canary attempt: %w", err)
	}
	tree, err := verbs.PrepareAttachmentTree(nil, attempt.root, "probe")
	if err != nil {
		return out, fmt.Errorf("prepare attachment canary tree: %w", err)
	}
	sessionRoot := filepath.Join(attempt.root, "attachments", "probe")
	if err := trustAttachmentCanaryDirs(
		attempt.root,
		filepath.Join(attempt.root, "attachments"),
		sessionRoot,
		filepath.Join(sessionRoot, "in"),
		filepath.Join(sessionRoot, "in", "staging"),
		filepath.Join(sessionRoot, "in", "journal"),
		filepath.Join(sessionRoot, "in", "published"),
		filepath.Join(sessionRoot, "in", "published", "sha256"),
		filepath.Join(sessionRoot, "out"),
		filepath.Join(sessionRoot, "out", "staging"),
		filepath.Join(sessionRoot, "out", "journal"),
		filepath.Join(sessionRoot, "out", "published"),
		filepath.Join(sessionRoot, "out", "published", "sha256"),
	); err != nil {
		return out, err
	}
	frame := verbs.AttachmentCanaryFrame{
		Version: verbs.AttachmentCanaryFrameVersion, Generation: generation,
	}
	frameDir := filepath.Join(attempt.root, "frame")
	if err := os.Mkdir(frameDir, 0o755); err != nil {
		return out, fmt.Errorf("create attachment canary frame directory: %w", err)
	}
	framePath := filepath.Join(frameDir, "frame.json")
	if err := writeAttachmentCanaryJSON(framePath, 0o444, frame); err != nil {
		return out, err
	}

	for _, source := range []string{tree.InboundPublished, tree.Outbound, frameDir} {
		if strings.Contains(source, ",") {
			return out, verbs.Domainf(
				"attachment canary source contains a Docker --mount delimiter",
			)
		}
	}
	createOut, createErr, err := attachmentCanaryDocker(
		"create",
		"--name", attempt.containerName,
		"--network", "none",
		"--user", "10002:10002",
		"--cap-drop", "ALL",
		"--label", "mc.attachment-canary="+generation,
		"--mount", "type=bind,src="+tree.InboundPublished+",dst=/mc/attachments/in,readonly",
		"--mount", "type=bind,src="+tree.Outbound+",dst=/mc/private/attachments/out",
		"--mount", "type=bind,src="+frameDir+",dst=/mc/attachment-canary,readonly",
		image,
		"sh", "-c", "while :; do sleep 3600; done",
	)
	if err != nil {
		return out, fmt.Errorf("create attachment canary container: %w: %s",
			err, strings.TrimSpace(string(createErr)))
	}
	containerID := strings.TrimSpace(string(createOut))
	if !attachmentCanaryContainerID.MatchString(containerID) {
		return out, verbs.Domainf("attachment canary create returned a non-canonical container id")
	}
	if err := inspectAttachmentCanary(attempt, containerID, tree, frameDir); err != nil {
		return out, err
	}
	if _, stderr, err := attachmentCanaryDocker("start", containerID); err != nil {
		return out, fmt.Errorf("start attachment canary container: %w: %s",
			err, strings.TrimSpace(string(stderr)))
	}
	if err := inspectPrivateAttachmentCanaryGate(containerID); err != nil {
		return out, err
	}

	inboundPayload := verbs.AttachmentCanaryInboundBytes()
	inboundDigest := verbs.AttachmentCanaryDigest(inboundPayload)
	inboundRelative, err := verbs.AttachmentCanaryRelativePath(inboundDigest)
	if err != nil {
		return out, err
	}
	inboundPath := filepath.Join(tree.InboundPublished, filepath.FromSlash(
		strings.TrimPrefix(inboundRelative, "published/"),
	))
	if err := publishHostAttachmentCanary(
		tree.InboundPublished, inboundPath, inboundPayload,
	); err != nil {
		return out, err
	}
	inboundEvidence, err := inspectHostAttachmentCanary(inboundPath, inboundPayload)
	if err != nil {
		return out, err
	}
	inboundContainerPath := "/mc/attachments/in/" +
		strings.TrimPrefix(inboundRelative, "published/")
	if err := readWarmAttachmentCanary(containerID, inboundContainerPath, inboundPayload); err != nil {
		return out, err
	}

	publisherOut, publisherErr, err := attachmentCanaryDocker(
		"exec", "--user", "10002:10002", containerID,
		"mc", "__attachment-canary-publish",
	)
	if err != nil {
		return out, fmt.Errorf("run fixed attachment canary publisher: %w: %s",
			err, strings.TrimSpace(string(publisherErr)))
	}
	var publisher verbs.AttachmentCanaryPublisherReceipt
	if err := decodeAttachmentCanaryJSON(publisherOut, &publisher); err != nil {
		return out, fmt.Errorf("decode attachment canary publisher receipt: %w", err)
	}
	outboundPayload, err := verbs.AttachmentCanaryOutboundBytes(frame)
	if err != nil {
		return out, err
	}
	outboundDigest := verbs.AttachmentCanaryDigest(outboundPayload)
	outboundRelative, err := verbs.AttachmentCanaryRelativePath(outboundDigest)
	if err != nil {
		return out, err
	}
	if publisher.Version != 1 || publisher.Generation != generation ||
		publisher.SHA256 != outboundDigest ||
		publisher.RelativePath != outboundRelative ||
		publisher.RealUID != 10002 || publisher.EffectiveUID != 10001 ||
		publisher.SavedUID != 10001 || publisher.FSUID != 10001 {
		return out, verbs.Domainf("attachment canary publisher returned a mismatched receipt")
	}
	outboundPath := filepath.Join(tree.Outbound, filepath.FromSlash(outboundRelative))
	outboundEvidence, err := inspectHostAttachmentCanary(outboundPath, outboundPayload)
	if err != nil {
		return out, err
	}
	denials, err := denyWarmAttachmentCanary(
		containerID,
		"/mc/private/attachments/out/"+outboundRelative,
	)
	if err != nil {
		return out, err
	}

	if _, stderr, err := attachmentCanaryDocker("restart", "--time", "10", containerID); err != nil {
		return out, fmt.Errorf("restart attachment canary container: %w: %s",
			err, strings.TrimSpace(string(stderr)))
	}
	if err := readWarmAttachmentCanary(containerID, inboundContainerPath, inboundPayload); err != nil {
		return out, fmt.Errorf("reopen inbound attachment after restart: %w", err)
	}
	if err := inspectAttachmentCanary(attempt, containerID, tree, frameDir); err != nil {
		return out, fmt.Errorf("reinspect attachment canary after restart: %w", err)
	}
	if err := inspectPrivateAttachmentCanaryGate(containerID); err != nil {
		return out, fmt.Errorf("reinspect attachment canary private gate after restart: %w", err)
	}
	restartedInbound, err := inspectHostAttachmentCanary(inboundPath, inboundPayload)
	if err != nil {
		return out, fmt.Errorf("reinspect inbound attachment after restart: %w", err)
	}
	restartedOutbound, err := inspectHostAttachmentCanary(outboundPath, outboundPayload)
	if err != nil {
		return out, fmt.Errorf("reopen outbound attachment after restart: %w", err)
	}
	if restartedInbound != inboundEvidence || restartedOutbound != outboundEvidence {
		return out, verbs.Domainf(
			"attachment canary object identity changed across container restart",
		)
	}

	out = verbs.AttachmentCanaryReceipt{
		Version: 1, Generation: generation,
		InboundSHA256: inboundDigest, OutboundSHA256: outboundDigest,
		RealUID: publisher.RealUID, EffectiveUID: publisher.EffectiveUID,
		SavedUID: publisher.SavedUID, FSUID: publisher.FSUID,
		WarmInboundRead: true, HostOutboundRead: true,
		PrivateGateHealthy: true,
		DeniedOutbound:     denials,
		RestartInboundRead: true, RestartOutboundRead: true,
		OutsideSessions: !pathWithin(filepath.Join(home, "sessions"), attempt.root),
	}
	if err := cleanupAttachmentCanaryAttempt(attempt); err != nil {
		return out, err
	}
	attemptLive = false
	if err := writeAttachmentCanaryAtomic(root, "last.json", 0o600, out); err != nil {
		return out, err
	}
	return out, nil
}

func ensureAttachmentCanaryDir(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create attachment canary directory %q: %w", path, err)
	}
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return fmt.Errorf("inspect attachment canary directory %q: %w", path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o777 != 0o700 ||
		int(stat.Uid) != os.Getuid() {
		return verbs.Domainf(
			"attachment canary directory %q must be operator-owned mode 0700",
			path,
		)
	}
	return nil
}

func trustAttachmentCanaryDirs(paths ...string) error {
	for _, path := range paths {
		if err := boundary.TrustHomeDir(path, os.Getuid()); err != nil {
			return verbs.Domainf(
				"attachment canary scratch ACL trust refused %q: %v",
				path, err,
			)
		}
	}
	return nil
}

func lockAttachmentCanary(root string) (*os.File, error) {
	path := filepath.Join(root, "lock")
	fd, err := unix.Open(path,
		unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open attachment canary lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		file.Close()
		return nil, fmt.Errorf("inspect attachment canary lock: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 ||
		int(stat.Uid) != os.Getuid() {
		file.Close()
		return nil, verbs.Domainf("attachment canary lock has unsafe identity or mode")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, verbs.Domainf("another attachment canary attempt is active")
	}
	return file, nil
}

func recoverAttachmentCanary(root string) error {
	activePath := filepath.Join(root, "active.json")
	body, err := readAttachmentCanaryFile(activePath, 0o600, 4096)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var active attachmentCanaryActive
	if err := decodeAttachmentCanaryJSON(body, &active); err != nil {
		return verbs.Domainf("attachment canary active journal is malformed")
	}
	if active.Version != 1 ||
		!regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(active.Generation) ||
		active.ContainerName != "mc-attachment-canary-"+active.Generation {
		return verbs.Domainf("attachment canary active journal is malformed")
	}
	return cleanupAttachmentCanaryAttempt(attachmentCanaryAttempt{
		root:          filepath.Join(root, "attempt-"+active.Generation),
		activePath:    activePath,
		generation:    active.Generation,
		containerName: active.ContainerName,
	})
}

func cleanupAttachmentCanaryAttempt(attempt attachmentCanaryAttempt) error {
	if err := removeAttachmentCanaryContainer(
		attempt.containerName, attempt.generation,
	); err != nil {
		return err
	}
	parent := filepath.Dir(attempt.root)
	if filepath.Base(attempt.root) != "attempt-"+attempt.generation ||
		filepath.Join(parent, filepath.Base(attempt.root)) != attempt.root {
		return verbs.Domainf("attachment canary cleanup root is not exact")
	}
	if info, err := os.Lstat(attempt.root); err == nil {
		var stat unix.Stat_t
		if unix.Lstat(attempt.root, &stat) != nil ||
			!info.IsDir() || info.Mode().Perm() != 0o700 ||
			int(stat.Uid) != os.Getuid() {
			return verbs.Domainf("attachment canary cleanup root changed identity or mode")
		}
		if err := os.RemoveAll(attempt.root); err != nil {
			return fmt.Errorf("remove exact attachment canary attempt: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect exact attachment canary attempt: %w", err)
	}
	if err := os.Remove(attempt.activePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove attachment canary active journal: %w", err)
	}
	return syncAttachmentCanaryDir(parent)
}

func removeAttachmentCanaryContainer(name, generation string) error {
	out, stderr, err := attachmentCanaryDocker("inspect", name)
	if err != nil {
		if dockerAttachmentCanaryAbsent(stderr) {
			return nil
		}
		return fmt.Errorf("inspect attachment canary cleanup target: %w: %s",
			err, strings.TrimSpace(string(stderr)))
	}
	var records []attachmentCanaryInspect
	if err := json.Unmarshal(out, &records); err != nil ||
		len(records) != 1 ||
		records[0].Config.Labels["mc.attachment-canary"] != generation {
		return verbs.Domainf("attachment canary cleanup target lost its exact label")
	}
	if _, stderr, err := attachmentCanaryDocker("rm", "-f", records[0].ID); err != nil {
		return fmt.Errorf("remove exact attachment canary container: %w: %s",
			err, strings.TrimSpace(string(stderr)))
	}
	if _, stderr, err := attachmentCanaryDocker("inspect", records[0].ID); err == nil ||
		!dockerAttachmentCanaryAbsent(stderr) {
		return verbs.Domainf("attachment canary container removal is ambiguous")
	}
	return nil
}

func inspectAttachmentCanary(
	attempt attachmentCanaryAttempt,
	containerID string,
	tree verbs.AttachmentTreeReceipt,
	frameDir string,
) error {
	out, stderr, err := attachmentCanaryDocker("inspect", containerID)
	if err != nil {
		return fmt.Errorf("inspect attachment canary container: %w: %s",
			err, strings.TrimSpace(string(stderr)))
	}
	var records []attachmentCanaryInspect
	if err := json.Unmarshal(out, &records); err != nil || len(records) != 1 {
		return verbs.Domainf("attachment canary inspect returned a malformed record")
	}
	record := records[0]
	if record.ID != containerID || record.Config.User != "10002:10002" ||
		record.Config.Labels["mc.attachment-canary"] != attempt.generation ||
		record.HostConfig.NetworkMode != "none" ||
		len(record.HostConfig.CapDrop) != 1 ||
		strings.ToUpper(record.HostConfig.CapDrop[0]) != "ALL" {
		return verbs.Domainf("attachment canary container envelope drifted")
	}
	for _, option := range record.HostConfig.SecurityOpt {
		if strings.Contains(strings.ToLower(option), "no-new-privileges") {
			return verbs.Domainf("attachment canary container disabled its setuid proof")
		}
	}
	return checkAttachmentCanaryMounts(record.Mounts, tree, frameDir)
}

// Split out of inspectAttachmentCanary so the closed mount table can be proven
// without a Docker seam: the enclosing function reaches Docker on its first
// line, so nothing above ever had a test.
func checkAttachmentCanaryMounts(
	mounts []attachmentCanaryMount,
	tree verbs.AttachmentTreeReceipt,
	frameDir string,
) error {
	want := map[string]struct {
		source string
		rw     bool
	}{
		"/mc/attachments/in": {
			source: normalizeDockerBindSource(tree.InboundPublished), rw: false,
		},
		"/mc/private/attachments/out": {
			source: normalizeDockerBindSource(tree.Outbound), rw: true,
		},
		"/mc/attachment-canary": {
			source: normalizeDockerBindSource(frameDir), rw: false,
		},
	}
	seen := map[string]bool{}
	for _, mount := range mounts {
		if mount.Destination == "/mc/private" {
			return verbs.Domainf("attachment canary replaced the image private gate")
		}
		expected, ok := want[mount.Destination]
		if !ok {
			continue
		}
		// Both sides normalized by one rule; see normalizeDockerBindSource.
		if mount.Type != "bind" ||
			normalizeDockerBindSource(mount.Source) != expected.source ||
			mount.RW != expected.rw {
			return verbs.Domainf("attachment canary inspect reported mount drift")
		}
		seen[mount.Destination] = true
	}
	if len(mounts) != len(want) {
		return verbs.Domainf("attachment canary inspect reported an extra mount")
	}
	for destination := range want {
		if !seen[destination] {
			return verbs.Domainf(
				"attachment canary inspect omitted fixed mount %s",
				destination,
			)
		}
	}
	return nil
}

func publishHostAttachmentCanary(root, path string, body []byte) error {
	if !pathWithin(root, path) {
		return verbs.Domainf("attachment canary inbound path escaped its published root")
	}
	shard := filepath.Dir(path)
	if err := os.Mkdir(shard, 0o700); err != nil {
		return fmt.Errorf("create attachment canary inbound shard: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create attachment canary inbound object: %w", err)
	}
	open := true
	defer func() {
		if open {
			_ = file.Close()
		}
	}()
	if _, err := file.Write(body); err != nil {
		return fmt.Errorf("write attachment canary inbound object: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync attachment canary inbound object: %w", err)
	}
	if err := file.Chmod(0o444); err != nil {
		return fmt.Errorf("seal attachment canary inbound object: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close attachment canary inbound object: %w", err)
	}
	open = false
	if err := syncAttachmentCanaryDir(shard); err != nil {
		return err
	}
	return syncAttachmentCanaryDir(filepath.Dir(shard))
}

func readWarmAttachmentCanary(containerID, path string, expected []byte) error {
	script := `
const path = ` + fmt.Sprintf("%q", path) + `;
const expected = Buffer.from(` + fmt.Sprintf("%q", base64.StdEncoding.EncodeToString(expected)) + `, "base64");
let actual;
for (let i = 0; i < 100; i++) {
  try {
    actual = Buffer.from(await Bun.file(path).arrayBuffer());
    if (actual.equals(expected)) break;
  } catch {}
  await Bun.sleep(25);
}
if (actual === undefined || !actual.equals(expected)) process.exit(41);
process.stdout.write("READ\n");
`
	stdout, stderr, err := attachmentCanaryDocker(
		"exec", "--user", "10002:10002", containerID,
		"bun", "-e", script,
	)
	if err != nil || strings.TrimSpace(string(stdout)) != "READ" {
		return fmt.Errorf("warm uid-10002 attachment read failed: %w: %s",
			err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

func inspectPrivateAttachmentCanaryGate(containerID string) error {
	script := `
import { lstat, readdir } from "node:fs/promises";
const path = "/mc/private";
const stat = await lstat(path);
let traverse = "OK";
try { await readdir(path); }
catch (error) { traverse = error?.code ?? "UNKNOWN"; }
process.stdout.write(JSON.stringify({
  is_directory: stat.isDirectory(),
  uid: stat.uid,
  gid: stat.gid,
  mode: stat.mode & 0o777,
  traverse,
}) + "\n");
`
	stdout, stderr, err := attachmentCanaryDocker(
		"exec", "--user", "10002:10002", containerID,
		"bun", "-e", script,
	)
	if err != nil {
		return fmt.Errorf("inspect live attachment canary private gate: %w: %s",
			err, strings.TrimSpace(string(stderr)))
	}
	var report attachmentCanaryGateReport
	if err := decodeAttachmentCanaryJSON(stdout, &report); err != nil {
		return fmt.Errorf("decode live attachment canary private gate: %w", err)
	}
	return validateAttachmentCanaryGateReport(report)
}

func validateAttachmentCanaryGateReport(report attachmentCanaryGateReport) error {
	if !report.IsDirectory || report.UID != 10001 || report.GID != 10001 ||
		report.Mode != 0o700 || report.Traverse != "EACCES" {
		return verbs.Domainf(
			"attachment canary private gate is not a 10001:10001 mode-0700 directory with uid-10002 EACCES",
		)
	}
	return nil
}

func denyWarmAttachmentCanary(containerID, outboundPath string) ([]string, error) {
	script := `
import { open, readdir, rename, unlink, writeFile } from "node:fs/promises";
const root = "/mc/private/attachments/out";
const object = ` + fmt.Sprintf("%q", outboundPath) + `;
const result = {};
async function probe(name, fn) {
  try { await fn(); result[name] = "OK"; }
  catch (error) { result[name] = error?.code ?? "UNKNOWN"; }
}
await probe("traverse", async () => { await readdir(root); });
await probe("open", async () => { const file = await open(object, "r"); await file.close(); });
await probe("create", async () => { await writeFile(root + "/model-create", "x"); });
await probe("rename", async () => { await rename(object, object + ".renamed"); });
await probe("unlink", async () => { await unlink(object); });
process.stdout.write(JSON.stringify(result) + "\n");
`
	stdout, stderr, err := attachmentCanaryDocker(
		"exec", "--user", "10002:10002", containerID,
		"bun", "-e", script,
	)
	if err != nil {
		return nil, fmt.Errorf("probe warm uid-10002 outbound denial: %w: %s",
			err, strings.TrimSpace(string(stderr)))
	}
	var result map[string]string
	if err := decodeAttachmentCanaryJSON(stdout, &result); err != nil {
		return nil, fmt.Errorf("decode attachment canary denial report: %w", err)
	}
	names := []string{"create", "open", "rename", "traverse", "unlink"}
	denials := make([]string, 0, len(names))
	for _, name := range names {
		if result[name] != "EACCES" {
			return nil, verbs.Domainf(
				"warm uid-10002 outbound %s returned %q, want EACCES",
				name, result[name],
			)
		}
		denials = append(denials, name+":EACCES")
	}
	sort.Strings(denials)
	return denials, nil
}

func inspectHostAttachmentCanary(
	path string,
	expected []byte,
) (attachmentCanaryHostEvidence, error) {
	var evidence attachmentCanaryHostEvidence
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return evidence, fmt.Errorf("inspect host attachment canary object: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o444 ||
		stat.Size != int64(len(expected)) {
		return evidence, verbs.Domainf(
			"host attachment canary object has the wrong type, mode, or size",
		)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return evidence, fmt.Errorf("host reopen attachment canary object: %w", err)
	}
	if !bytes.Equal(body, expected) {
		return evidence, verbs.Domainf("host attachment canary object bytes changed")
	}
	return attachmentCanaryHostEvidence{
		device: uint64(stat.Dev),
		inode:  uint64(stat.Ino),
		size:   stat.Size,
	}, nil
}

func attachmentCanaryDocker(args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout := boundedDoctorBuffer{limit: attachmentCanaryOutputLimit}
	stderr := boundedDoctorBuffer{limit: attachmentCanaryOutputLimit}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("docker command timed out")
	}
	if stdout.overflow || stderr.overflow {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("docker command exceeded its output bound")
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

func dockerAttachmentCanaryAbsent(stderr []byte) bool {
	text := strings.ToLower(string(stderr))
	return strings.Contains(text, "no such object") ||
		strings.Contains(text, "no such container")
}

func writeAttachmentCanaryJSON(path string, mode os.FileMode, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode attachment canary JSON: %w", err)
	}
	body = append(body, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create attachment canary JSON: %w", err)
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return fmt.Errorf("write attachment canary JSON: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync attachment canary JSON: %w", err)
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return fmt.Errorf("mode attachment canary JSON: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close attachment canary JSON: %w", err)
	}
	return syncAttachmentCanaryDir(filepath.Dir(path))
}

func writeAttachmentCanaryAtomic(
	root, name string,
	mode os.FileMode,
	value any,
) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode attachment canary receipt: %w", err)
	}
	body = append(body, '\n')
	stage := filepath.Join(root, "."+name+".next")
	_ = os.Remove(stage)
	file, err := os.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create attachment canary receipt stage: %w", err)
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return fmt.Errorf("write attachment canary receipt stage: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync attachment canary receipt stage: %w", err)
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return fmt.Errorf("mode attachment canary receipt stage: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close attachment canary receipt stage: %w", err)
	}
	if err := os.Rename(stage, filepath.Join(root, name)); err != nil {
		return fmt.Errorf("publish attachment canary receipt: %w", err)
	}
	return syncAttachmentCanaryDir(root)
}

func readAttachmentCanaryFile(path string, mode os.FileMode, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return nil, verbs.Domainf("attachment canary file %q has unsafe type or mode", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, verbs.Domainf("attachment canary file %q exceeds its bound", path)
	}
	return body, nil
}

func decodeAttachmentCanaryJSON(body []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func syncAttachmentCanaryDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open attachment canary directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync attachment canary directory: %w", err)
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
