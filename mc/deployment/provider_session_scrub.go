package deployment

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

var legacyCredentialField = regexp.MustCompile(`(?i)\\*"(?:access_?token|refresh_?token|id_?token|api_?key)\\*"\s*:\s*\\*"([^"\\\r\n]{8,})\\*"`)
var legacyCredentialToken = regexp.MustCompile(`(?:sk-[A-Za-z0-9_-]{16,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|culler_proxy_[A-Za-z0-9_-]{43})`)
var legacyCredentialEnvironment = regexp.MustCompile(`[A-Z][A-Z0-9_]*(?:API_KEY|AUTH_TOKEN|OAUTH_TOKEN|ACCESS_TOKEN|REFRESH_TOKEN|ID_TOKEN)=([A-Za-z0-9_./+=~-]{8,})`)
var legacyEncodedToken = regexp.MustCompile(`[A-Za-z0-9_+/=%.-]{40,}`)
var legacyRunEnvelopeName = regexp.MustCompile(`^(?:[a-f0-9]{16}|h-[a-z0-9-]+)\.json$`)

type providerScrubValues struct {
	values map[string]bool
	bytes  int
}

func (s *providerScrubValues) add(value string) error {
	if len(value) < 8 || s.values[value] {
		return nil
	}
	if len(value) > 256<<10 || len(s.values) >= 4096 || s.bytes+len(value) > 4<<20 {
		return fmt.Errorf("The previous credential set exceeds its scrub limit.")
	}
	s.values[value] = true
	s.bytes += len(value)
	return nil
}

func (s *providerScrubValues) discover(body []byte, encoded bool) (bool, error) {
	found := false
	for _, pattern := range []*regexp.Regexp{legacyCredentialField, legacyCredentialEnvironment} {
		for _, match := range pattern.FindAllSubmatch(body, -1) {
			found = true
			if err := s.add(string(match[1])); err != nil {
				return false, err
			}
		}
	}
	for _, value := range legacyCredentialToken.FindAll(body, -1) {
		found = true
		if err := s.add(string(value)); err != nil {
			return false, err
		}
	}
	if encoded {
		for _, value := range legacyEncodedToken.FindAll(body, -1) {
			if len(value) > 256<<10 {
				continue
			}
			var decoded [][]byte
			for _, format := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				if raw, err := format.DecodeString(string(value)); err == nil {
					decoded = append(decoded, raw)
				}
			}
			if strings.Contains(string(value), "%") {
				if raw, err := url.PathUnescape(string(value)); err == nil {
					decoded = append(decoded, []byte(raw))
				}
			}
			for _, raw := range decoded {
				secret, err := s.discover(raw, false)
				if err != nil {
					return false, err
				}
				if secret {
					found = true
					if err := s.add(string(value)); err != nil {
						return false, err
					}
				}
			}
		}
	}
	return found, nil
}

func (s *providerScrubValues) patterns() [][]byte {
	all := map[string]bool{}
	for value := range s.values {
		quoted, _ := json.Marshal(value)
		for _, item := range []string{value, string(quoted[1 : len(quoted)-1]), url.PathEscape(value),
			base64.StdEncoding.EncodeToString([]byte(value)), base64.RawStdEncoding.EncodeToString([]byte(value)),
			base64.URLEncoding.EncodeToString([]byte(value)), base64.RawURLEncoding.EncodeToString([]byte(value))} {
			all[item] = true
		}
		var percent, unicode strings.Builder
		for _, b := range []byte(value) {
			fmt.Fprintf(&percent, "%%%02x", b)
			fmt.Fprintf(&unicode, "\\u%04x", b)
		}
		all[percent.String()], all[strings.ToUpper(percent.String())] = true, true
		all[unicode.String()] = true
		all[strings.ReplaceAll(strings.ToUpper(unicode.String()), "\\U", "\\u")] = true
	}
	values := make([]string, 0, len(all))
	for value := range all {
		values = append(values, value)
	}
	for _, value := range values {
		for range 2 {
			quoted, _ := json.Marshal(value)
			value = string(quoted[1 : len(quoted)-1])
			all[value] = true
		}
	}
	patterns := make([][]byte, 0, len(all))
	for value := range all {
		patterns = append(patterns, []byte(value))
	}
	sort.Slice(patterns, func(i, j int) bool { return len(patterns[i]) > len(patterns[j]) })
	return patterns
}

func scrubPreviousProviderSessions(home string) error {
	values := &providerScrubValues{values: map[string]bool{}}
	entries, err := os.ReadDir(filepath.Join(home, "refresh-grants"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		access, err := ReadProviderCredential(home, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return err
		}
		for _, value := range []string{access.Record.AccessToken, access.Record.RefreshToken, access.Record.IDToken, access.Record.APIKey} {
			if err := values.add(value); err != nil {
				return err
			}
		}
	}
	// The first pass learns old token values before a raw credential file disappears.
	if err := walkProviderSessionFiles(home, func(_ int, _ string, body []byte, _ os.FileMode) error {
		_, err := values.discover(body, true)
		return err
	}); err != nil {
		return err
	}
	patterns := values.patterns()
	return walkProviderSessionFiles(home, func(parent int, name string, body []byte, mode os.FileMode) error {
		changed := false
		for _, pattern := range patterns {
			for offset := 0; offset < len(body); {
				at := bytes.Index(body[offset:], pattern)
				if at < 0 {
					break
				}
				at += offset
				for index := at; index < at+len(pattern); index++ {
					body[index] = '*'
				}
				offset, changed = at+len(pattern), true
			}
		}
		if !changed {
			return nil
		}
		return replaceProviderSessionFile(parent, name, body, mode)
	})
}

func walkProviderSessionFiles(home string, visit func(int, string, []byte, os.FileMode) error) error {
	root, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(root)
	files, total := 0, int64(0)
	var walk func(int, string, int) error
	walk = func(parent int, relative string, depth int) error {
		if depth > 12 {
			return fmt.Errorf("The previous session exceeds its directory limit.")
		}
		names, err := runtimeAuthDirectoryEntries(parent, "previous provider session")
		if err != nil || len(names) > 100_000 {
			return fmt.Errorf("The previous session directory is unavailable or exceeds its entry limit.")
		}
		for _, name := range names {
			path := relative + "/" + name.Name()
			if relative == "runs" && !legacyRunCredentialName.MatchString(name.Name()) && !legacyRunEnvelopeName.MatchString(name.Name()) {
				continue
			}
			if relative == "agentsview" && name.Name() != "traces" {
				continue
			}
			if strings.HasPrefix(relative, "environment-runs/") && depth == 1 && name.Name() != "session" && name.Name() != "auth.json" && name.Name() != "run.json" {
				continue
			}
			var info unix.Stat_t
			if err := unix.Fstatat(parent, name.Name(), &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			if info.Mode&unix.S_IFMT == unix.S_IFDIR {
				child, err := openProviderArtifactDirectory(parent, name.Name())
				if err != nil {
					return err
				}
				err = walk(child, path, depth+1)
				unix.Close(child)
				if err != nil {
					return err
				}
				continue
			}
			if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 || int(info.Uid) != os.Getuid() {
				return fmt.Errorf("The previous session contains an unsafe file.")
			}
			if !strings.HasSuffix(name.Name(), ".jsonl") && !strings.HasSuffix(name.Name(), ".ndjson") && !strings.HasSuffix(name.Name(), ".json") {
				continue
			}
			files++
			total += info.Size
			if files > 100_000 || info.Size > 64<<20 || total > 2<<30 {
				return fmt.Errorf("The previous session data exceeds its scrub limit.")
			}
			fd, err := unix.Openat(parent, name.Name(), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			file := os.NewFile(uintptr(fd), "previous provider session")
			var opened unix.Stat_t
			if err := unix.Fstat(fd, &opened); err != nil || opened.Dev != info.Dev || opened.Ino != info.Ino || opened.Nlink != 1 || opened.Uid != info.Uid {
				file.Close()
				return fmt.Errorf("The previous session changed during its scrub.")
			}
			body, readErr := io.ReadAll(io.LimitReader(file, 64<<20+1))
			file.Close()
			if readErr != nil || int64(len(body)) != info.Size {
				return fmt.Errorf("The previous session changed during its scrub.")
			}
			if err := visit(parent, name.Name(), body, os.FileMode(info.Mode&0777)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, directory := range []string{"runs", "sessions", "environment-runs", "agentsview", "logs"} {
		fd, err := openProviderArtifactDirectory(root, directory)
		if err == unix.ENOENT {
			continue
		}
		if err != nil {
			return err
		}
		err = walk(fd, directory, 0)
		unix.Close(fd)
		if err != nil {
			return err
		}
	}
	return nil
}

func replaceProviderSessionFile(parent int, name string, body []byte, mode os.FileMode) error {
	nonce, err := newLiveProviderAttemptID()
	if err != nil {
		return err
	}
	stage := ".provider-scrub-" + nonce
	fd, err := unix.Openat(parent, stage, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(parent, stage, 0)
	file := os.NewFile(uintptr(fd), "previous provider session scrub")
	if _, err = file.Write(body); err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = unix.Renameat(parent, stage, parent, name)
	}
	if err == nil {
		err = unix.Fsync(parent)
	}
	return err
}
