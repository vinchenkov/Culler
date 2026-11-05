//go:build linux && !test_fake_routing

package verbs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const attachmentCanaryFramePath = "/mc/attachment-canary/frame.json"
const attachmentCanaryOutboundRoot = "/mc/private/attachments/out"

// AttachmentCanaryPublish is a canary-only, fixed-shape write through the
// production setuid gate. It accepts no path or payload and requires an exact
// ephemeral frame mount that production Homie containers do not carry.
func AttachmentCanaryPublish(id *RunIdentity) (
	out AttachmentCanaryPublisherReceipt,
	retErr error,
) {
	if err := RequireHostScope(id, "attachment canary publisher"); err != nil {
		return out, err
	}
	realUID, effectiveUID, savedUID, fsUID, err := attachmentCanaryUIDs()
	if err != nil {
		return out, err
	}
	if realUID != 10002 || effectiveUID != 10001 ||
		savedUID != 10001 || fsUID != 10001 {
		return out, Domainf(
			"attachment canary publisher requires ruid 10002 and euid/suid/fsuid 10001",
		)
	}
	frame, err := readAttachmentCanaryFrame()
	if err != nil {
		return out, err
	}
	payload, err := AttachmentCanaryOutboundBytes(frame)
	if err != nil {
		return out, err
	}
	digest := AttachmentCanaryDigest(payload)
	relative, err := AttachmentCanaryRelativePath(digest)
	if err != nil {
		return out, err
	}

	root, err := unix.Open(attachmentCanaryOutboundRoot,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return out, fmt.Errorf("open fixed attachment canary outbound root: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(root)) }()
	staging, err := openCanaryDirAt(root, "staging")
	if err != nil {
		return out, err
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(staging)) }()
	published, err := openCanaryDirAt(root, "published")
	if err != nil {
		return out, err
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(published)) }()
	shaDir, err := openCanaryDirAt(published, "sha256")
	if err != nil {
		return out, err
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(shaDir)) }()
	shard, err := openOrCreateCanaryDirAt(shaDir, digest[:2])
	if err != nil {
		return out, err
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(shard)) }()

	stageName := ".canary-" + frame.Generation
	stageFD, err := unix.Openat(staging, stageName,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600)
	if err != nil {
		return out, fmt.Errorf("create fixed attachment canary stage: %w", err)
	}
	stageOpen := true
	defer func() {
		if stageOpen {
			retErr = errors.Join(retErr, unix.Close(stageFD))
		}
	}()
	if err := writeCanaryBytes(stageFD, payload); err != nil {
		return out, err
	}
	if err := unix.Fsync(stageFD); err != nil {
		return out, fmt.Errorf("sync attachment canary stage: %w", err)
	}
	if err := unix.Fchmod(stageFD, 0o444); err != nil {
		return out, fmt.Errorf("seal attachment canary stage: %w", err)
	}
	if err := unix.Close(stageFD); err != nil {
		return out, fmt.Errorf("close attachment canary stage: %w", err)
	}
	stageOpen = false
	if err := unix.Renameat2(staging, stageName, shard, digest, unix.RENAME_NOREPLACE); err != nil {
		return out, fmt.Errorf("publish fixed attachment canary object: %w", err)
	}
	if err := unix.Fsync(shard); err != nil {
		return out, fmt.Errorf("sync attachment canary shard: %w", err)
	}
	if err := unix.Fsync(staging); err != nil {
		return out, fmt.Errorf("sync attachment canary staging: %w", err)
	}

	return AttachmentCanaryPublisherReceipt{
		Version: 1, Generation: frame.Generation,
		SHA256: digest, RelativePath: relative,
		RealUID: realUID, EffectiveUID: effectiveUID,
		SavedUID: savedUID, FSUID: fsUID,
	}, nil
}

func readAttachmentCanaryFrame() (AttachmentCanaryFrame, error) {
	var frame AttachmentCanaryFrame
	fd, err := unix.Open(attachmentCanaryFramePath,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return frame, Domainf("attachment canary frame is absent or unreachable")
	}
	file := os.NewFile(uintptr(fd), attachmentCanaryFramePath)
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return frame, fmt.Errorf("inspect attachment canary frame: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o444 ||
		stat.Size < 2 || stat.Size > 4096 {
		return frame, Domainf("attachment canary frame has the wrong type, mode, or size")
	}
	body, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return frame, fmt.Errorf("read attachment canary frame: %w", err)
	}
	if len(body) > 4096 {
		return frame, Domainf("attachment canary frame exceeds its bound")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&frame); err != nil {
		return frame, Domainf("attachment canary frame is malformed")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return frame, Domainf("attachment canary frame has trailing content")
	}
	if err := ValidateAttachmentCanaryFrame(frame); err != nil {
		return frame, err
	}
	return frame, nil
}

func attachmentCanaryUIDs() (int, int, int, int, error) {
	body, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("read attachment canary process status: %w", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
		if len(fields) != 4 {
			break
		}
		values := make([]int, 4)
		for i, field := range fields {
			value, err := strconv.Atoi(field)
			if err != nil {
				return 0, 0, 0, 0, fmt.Errorf("parse attachment canary uid status: %w", err)
			}
			values[i] = value
		}
		return values[0], values[1], values[2], values[3], nil
	}
	return 0, 0, 0, 0, Domainf("attachment canary process has no exact uid status")
}

func openCanaryDirAt(parent int, name string) (int, error) {
	fd, err := unix.Openat(parent, name,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open fixed attachment canary directory %q: %w", name, err)
	}
	return fd, nil
}

func openOrCreateCanaryDirAt(parent int, name string) (int, error) {
	if err := unix.Mkdirat(parent, name, 0o700); err != nil &&
		!errors.Is(err, unix.EEXIST) {
		return -1, fmt.Errorf("create fixed attachment canary directory %q: %w", name, err)
	}
	return openCanaryDirAt(parent, name)
}

func writeCanaryBytes(fd int, body []byte) error {
	for len(body) > 0 {
		n, err := unix.Write(fd, body)
		if err != nil {
			return fmt.Errorf("write attachment canary stage: %w", err)
		}
		if n <= 0 {
			return fmt.Errorf("write attachment canary stage: short write")
		}
		body = body[n:]
	}
	return nil
}
