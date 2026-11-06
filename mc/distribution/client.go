package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var ErrNoRelease = errors.New("no complete Culler release is available")
var ErrAccess = errors.New("GitHub access failed; renew the repository and package credentials, then run install.sh again")
var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type Client struct {
	Repository string
	Token      string
	HTTP       *http.Client
	BaseURL    string
}
type releaseAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}
type githubRelease struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}
type Workflow struct {
	State  string `json:"state"`
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
}

func NewClient(repository, token string) *Client {
	if repository == "" {
		repository = DefaultRepository
	}
	return &Client{Repository: repository, Token: token, BaseURL: "https://api.github.com", HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) get(ctx context.Context, path, accept string, limit int64) ([]byte, error) {
	if !repositoryName.MatchString(c.Repository) {
		return nil, errors.New("GitHub repository must use owner/name")
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Host == "" {
		return nil, errors.New("GitHub API address is invalid")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Cache-Control", "no-cache")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	httpClient := http.Client{Timeout: 30 * time.Second}
	if c.HTTP != nil {
		httpClient = *c.HTTP
	}
	originalRedirect := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" && base.Scheme == "https" {
			return errors.New("GitHub redirected to an insecure download")
		}
		if r.URL.Host != base.Host {
			r.Header.Del("Authorization")
		}
		if len(via) >= 10 {
			return errors.New("too many GitHub redirects")
		}
		if originalRedirect != nil {
			return originalRedirect(r, via)
		}
		return nil
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.New("GitHub could not be reached; check the connection and retry")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return nil, ErrAccess
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNoRelease
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned HTTP %d; retry the release check", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, errors.New("GitHub download was interrupted; retry")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("GitHub response exceeds its size limit")
	}
	return data, nil
}

func (c *Client) release(ctx context.Context, tag string) (githubRelease, error) {
	path := "/repos/" + c.Repository + "/releases/latest"
	if tag != "" {
		path = "/repos/" + c.Repository + "/releases/tags/" + url.PathEscape(tag)
	}
	data, err := c.get(ctx, path, "application/vnd.github+json", 2<<20)
	if err != nil {
		return githubRelease{}, err
	}
	var r githubRelease
	if json.Unmarshal(data, &r) != nil || r.Draft || r.Prerelease {
		return r, ErrNoRelease
	}
	return r, nil
}
func findAsset(r githubRelease, name string) (releaseAsset, error) {
	var a releaseAsset
	for _, candidate := range r.Assets {
		if candidate.Name == name {
			if a.ID != 0 {
				return a, errors.New("release contains duplicate assets")
			}
			a = candidate
		}
	}
	if a.ID <= 0 {
		return a, errors.New("release asset is absent")
	}
	return a, nil
}
func (c *Client) Latest(ctx context.Context) (Record, error) {
	release, err := c.release(ctx, "")
	if errors.Is(err, ErrNoRelease) {
		_, accessErr := c.get(ctx, "/repos/"+c.Repository, "application/vnd.github+json", 1<<20)
		if errors.Is(accessErr, ErrNoRelease) || errors.Is(accessErr, ErrAccess) {
			return Record{}, ErrAccess
		}
		if accessErr != nil {
			return Record{}, accessErr
		}
	}
	if err != nil {
		return Record{}, err
	}
	asset, err := findAsset(release, RecordName)
	if err != nil {
		return Record{}, err
	}
	data, err := c.get(ctx, fmt.Sprintf("/repos/%s/releases/assets/%d", c.Repository, asset.ID), "application/octet-stream", 1<<20)
	if err != nil {
		return Record{}, err
	}
	record, err := Decode(data)
	if err != nil {
		return Record{}, err
	}
	if record.Tag != release.Tag {
		return Record{}, errors.New("release record and GitHub tag disagree")
	}
	assets := []Asset{record.Installer, record.Package}
	if record.OpenCode != nil {
		assets = append(assets, *record.OpenCode)
	}
	for _, a := range assets {
		found, err := findAsset(release, a.Name)
		if err != nil {
			return Record{}, err
		}
		if found.Size != a.Size {
			return Record{}, errors.New("release asset size does not match its record")
		}
	}
	return record, nil
}

// Download retains the selected release tag if a newer release becomes available.
func (c *Client) Download(ctx context.Context, r Record, a Asset, destination string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if a != r.Installer && a != r.Package && (r.OpenCode == nil || a != *r.OpenCode) {
		return errors.New("asset does not belong to selected release")
	}
	release, err := c.release(ctx, r.Tag)
	if err != nil {
		return err
	}
	if release.Tag != r.Tag {
		return errors.New("GitHub returned another release")
	}
	asset, err := findAsset(release, a.Name)
	if err != nil {
		return err
	}
	if asset.Size != a.Size {
		return errors.New("release asset size changed")
	}
	data, err := c.get(ctx, fmt.Sprintf("/repos/%s/releases/assets/%d", c.Repository, asset.ID), "application/octet-stream", a.Size)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != a.Size || hex.EncodeToString(digest[:]) != a.SHA256 {
		return errors.New("release asset failed verification; run install.sh again")
	}
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		os.Remove(destination)
		return err
	}
	return closeErr
}
func (c *Client) Workflow(ctx context.Context) (Workflow, error) {
	data, err := c.get(ctx, "/repos/"+c.Repository+"/commits/main", "application/vnd.github+json", 1<<20)
	if err != nil {
		return Workflow{}, err
	}
	var head struct {
		SHA string `json:"sha"`
	}
	if json.Unmarshal(data, &head) != nil || !commitDigest.MatchString(head.SHA) {
		return Workflow{}, errors.New("GitHub main commit is invalid")
	}
	data, err = c.get(ctx, "/repos/"+c.Repository+"/actions/workflows/release.yml/runs?branch=main&event=push&head_sha="+head.SHA+"&per_page=1", "application/vnd.github+json", 1<<20)
	if err != nil {
		return Workflow{}, err
	}
	var response struct {
		Runs []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			URL        string `json:"html_url"`
			Commit     string `json:"head_sha"`
		} `json:"workflow_runs"`
	}
	if json.Unmarshal(data, &response) != nil {
		return Workflow{}, errors.New("GitHub workflow response is invalid")
	}
	if len(response.Runs) == 0 || response.Runs[0].Commit != head.SHA {
		return Workflow{State: "unknown", Commit: head.SHA}, nil
	}
	run := response.Runs[0]
	state := "preparing"
	if run.Status == "completed" {
		state = "failed"
		switch run.Conclusion {
		case "success":
			state = "published"
		case "cancelled":
			state = "cancelled"
		}
	}
	return Workflow{State: state, URL: run.URL, Commit: run.Commit}, nil
}
func (c *Client) Username(ctx context.Context) (string, error) {
	data, err := c.get(ctx, "/user", "application/vnd.github+json", 1<<20)
	if err != nil {
		return "", err
	}
	var user struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(data, &user) != nil || user.Login == "" {
		return "", ErrAccess
	}
	return user.Login, nil
}

func Credential(ctx context.Context) (string, error) {
	path := os.Getenv("CULLER_GITHUB_TOKEN_FILE")
	explicit := path != ""
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".config", "culler", "github-token")
	}
	info, err := os.Lstat(path)
	if err == nil {
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || int(owner.Uid) != os.Getuid() || owner.Nlink != 1 {
			return "", errors.New("GitHub token file must be a private, operator-owned regular file")
		}
		fd, openErr := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if openErr != nil {
			return "", errors.New("GitHub token file cannot be read")
		}
		file := os.NewFile(uintptr(fd), path)
		defer file.Close()
		opened, statErr := file.Stat()
		if statErr != nil || !os.SameFile(info, opened) {
			return "", errors.New("GitHub token file changed during access")
		}
		data, err := io.ReadAll(io.LimitReader(file, 16385))
		if err != nil || len(data) > 16384 {
			return "", errors.New("GitHub token file cannot be read")
		}
		token := strings.TrimSpace(string(data))
		if token == "" || strings.ContainsAny(token, "\r\n") {
			return "", errors.New("GitHub token file is empty or invalid")
		}
		return token, nil
	}
	if !os.IsNotExist(err) || explicit {
		return "", errors.New("GitHub token file is unavailable")
	}
	command, lookupErr := exec.LookPath("gh")
	if lookupErr != nil {
		home, _ := os.UserHomeDir()
		for _, candidate := range []string{filepath.Join(home, ".local", "bin", "gh"), "/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				command = candidate
				break
			}
		}
	}
	if command == "" {
		return "", nil
	}
	out, err := exec.CommandContext(ctx, command, "auth", "token", "--hostname", "github.com").Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	return "", nil
}
