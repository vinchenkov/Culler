package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	operatorconfig "mc/config"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
	"mc/distribution"
)

var regexpContainerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

type UpdatePath struct {
	Path    string `json:"path"`
	Backup  string `json:"backup"`
	Present bool   `json:"present"`
	SHA256  string `json:"sha256,omitempty"`
	Bytes   int64  `json:"bytes"`
}
type UpdateVolume struct {
	Name   string            `json:"name"`
	Backup string            `json:"backup"`
	SHA256 string            `json:"sha256"`
	Labels map[string]string `json:"labels"`
}
type UpdateCheckpoint struct {
	Root          string         `json:"root"`
	Paths         []UpdatePath   `json:"paths"`
	Volumes       []UpdateVolume `json:"volumes"`
	AbsentVolumes []string       `json:"absent_volumes"`
	Preserved     []string       `json:"preserved"`
}

func updateHostPaths(home string, j *UpdateJournal) []string {
	names := []string{"config.toml", "deployment.uuid", "release", "supervision", "bin", "state", "agentsview/data", "agentsview/token", "agentsview/home"}
	paths := make([]string, 0, len(names)+1)
	for _, name := range names {
		paths = append(paths, filepath.Join(home, name))
	}
	if j.BinDir != "" {
		paths = append(paths, filepath.Join(j.BinDir, "culler"))
		paths = append(paths, filepath.Join(j.BinDir, "mc"))
	}
	if j.Previous != nil {
		userHome, _ := os.UserHomeDir()
		names, _ := RuntimeNames(home)
		for _, unit := range SupervisedUnits {
			paths = append(paths, filepath.Join(userHome, "Library", "LaunchAgents", UnitLabel(names, unit)+".plist"))
		}
	}
	return paths
}

func (c *UpdateCheckpoint) validate(home string, j *UpdateJournal) error {
	if c.Root != filepath.Join(UpdateDirectory(home, j.ID), "checkpoint") {
		return errors.New("The update checkpoint path is invalid.")
	}
	allowed := updateHostPaths(home, j)
	if len(c.Paths) != len(allowed) {
		return errors.New("The update checkpoint has incomplete host coverage.")
	}
	for i, p := range c.Paths {
		if p.Path != allowed[i] || p.Backup != filepath.Join(c.Root, "host", fmt.Sprint(i)) || p.Bytes < 0 || (p.Present && !validSHA256(p.SHA256)) {
			return errors.New("The update checkpoint has an invalid host path.")
		}
	}
	names, err := RuntimeNames(home)
	if err != nil {
		return err
	}
	optional, _ := OpenCodeVolume(home)
	seen := map[string]bool{}
	for _, v := range c.Volumes {
		if seen[v.Name] || (v.Name != names.Volume && v.Name != optional) || v.Backup != v.Name+"-update-"+j.ID || !validSHA256(v.SHA256) {
			return errors.New("The update checkpoint has an invalid volume.")
		}
		seen[v.Name] = true
	}
	for _, name := range c.AbsentVolumes {
		if name != optional || seen[name] {
			return errors.New("The update checkpoint has an invalid absent volume.")
		}
		seen[name] = true
	}
	if j.Previous != nil && (!seen[names.Volume] || !seen[optional]) {
		return errors.New("The update checkpoint has incomplete volume coverage.")
	}
	return nil
}

func RetainUpdateCandidate(home string, j *UpdateJournal, source string) error {
	if err := j.validate(home); err != nil {
		return err
	}
	if err := ensureUpdateRoot(home); err != nil {
		return err
	}
	root := UpdateDirectory(home, j.ID)
	if err := ensureOwnerDirectory(root, "update artifacts"); err != nil {
		return err
	}
	target := j.CandidateRoot
	if source == target || j.CandidateTreeSHA256 != "" {
		sum, _, err := updateTreeDigest(target)
		if err != nil {
			return err
		}
		if sum != j.CandidateTreeSHA256 {
			return errors.New("The retained candidate failed its checksum. Keep Culler stopped and repair the update artifacts.")
		}
		return nil
	}
	stage := filepath.Join(root, "candidate.next")
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := copyUpdatePath(source, stage); err != nil {
		return err
	}
	if err := distribution.Save(filepath.Join(stage, "release.json"), j.Candidate); err != nil {
		return err
	}
	executable, err := CLIFileDigest(filepath.Join(stage, "bin", "mc"))
	if err != nil {
		return err
	}
	digest, _, err := updateTreeDigest(stage)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return errors.New("An unrecorded candidate already exists. Keep it for recovery inspection.")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		return err
	}
	if err := syncDirectory(root); err != nil {
		return err
	}
	j.CandidateSHA256, j.CandidateTreeSHA256 = executable, digest
	return nil
}

func CreateUpdateCheckpoint(ctx context.Context, home string, j *UpdateJournal, run RuntimeDocker) error {
	run = updateOwnedDocker(j, run)
	if j.Checkpoint != nil {
		return verifyUpdateCheckpoint(ctx, home, j, run)
	}
	if err := j.validate(home); err != nil {
		return err
	}
	if run == nil {
		run = runRuntimeDocker
	}
	root := filepath.Join(UpdateDirectory(home, j.ID), "checkpoint")
	if body, err := os.ReadFile(filepath.Join(root, "manifest.json")); err == nil {
		var retained UpdateCheckpoint
		if json.Unmarshal(body, &retained) != nil {
			return errors.New("The retained checkpoint manifest is invalid.")
		}
		j.Checkpoint = &retained
		return verifyUpdateCheckpoint(ctx, home, j, run)
	} else if !os.IsNotExist(err) {
		return err
	}
	stage := root + ".next"
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(stage, "host"), 0700); err != nil {
		return err
	}
	checkpoint := &UpdateCheckpoint{Root: root, Paths: []UpdatePath{}, Volumes: []UpdateVolume{}, Preserved: []string{
		"Project repositories and worktrees", "Project homes", "sessions", "runs", "environment-runs", "refresh-grants", "agent-profile-operations", "Keychain credentials", "artifacts", "agentsview/traces", "wiki source", "selected Project images", "user backups",
	}}
	var required int64
	for i, path := range updateHostPaths(home, j) {
		if err := validateUpdatePathParents(path); err != nil {
			return err
		}
		sum, size, err := updateTreeDigest(path)
		present := err == nil
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		checkpoint.Paths = append(checkpoint.Paths, UpdatePath{Path: path, Backup: filepath.Join(root, "host", fmt.Sprint(i)), Present: present, SHA256: sum, Bytes: size})
		required += size
	}
	if err := updateDiskSpace(home, required*2+(64<<20)); err != nil {
		return err
	}
	for i, p := range checkpoint.Paths {
		if !p.Present {
			continue
		}
		destination := filepath.Join(stage, "host", fmt.Sprint(i))
		if err := copyUpdatePath(p.Path, destination); err != nil {
			return err
		}
		sum, _, err := updateTreeDigest(destination)
		if err != nil {
			return err
		}
		if sum != p.SHA256 {
			return errors.New("A host checkpoint copy failed its checksum.")
		}
	}
	if j.Previous != nil {
		names, _ := RuntimeNames(home)
		optional, _ := OpenCodeVolume(home)
		for _, name := range []string{names.Volume, optional} {
			record, exists, err := inspectUpdateVolume(ctx, name, run)
			if err != nil {
				return err
			}
			if !exists {
				if name == names.Volume {
					return errors.New("The previous spine volume is absent.")
				}
				checkpoint.AbsentVolumes = append(checkpoint.AbsentVolumes, name)
				continue
			}
			if err := requireUpdateVolumeIdle(ctx, name, run); err != nil {
				return err
			}
			digest, err := updateVolumeDigest(ctx, j.Previous.Image, name, run)
			if err != nil {
				return err
			}
			backup := name + "-update-" + j.ID
			if err := ensureUpdateBackupVolume(ctx, backup, j, run); err != nil {
				return err
			}
			if err := copyUpdateVolume(ctx, j.Previous.Image, name, backup, run); err != nil {
				return err
			}
			actual, err := updateVolumeDigest(ctx, j.Previous.Image, backup, run)
			if err != nil {
				return err
			}
			if actual != digest {
				return errors.New("The volume checkpoint failed its checksum.")
			}
			if name == names.Volume {
				if err := checkUpdateSpine(ctx, j.Previous.Image, backup, j.Previous.Schema, run); err != nil {
					return err
				}
			}
			checkpoint.Volumes = append(checkpoint.Volumes, UpdateVolume{Name: name, Backup: backup, SHA256: digest, Labels: record.Labels})
		}
	}
	body, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	if err := writeUpdateFile(filepath.Join(stage, "manifest.json"), append(body, '\n'), 0600); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(stage, "host")); err != nil {
		return err
	}
	if _, err := os.Lstat(root); err == nil {
		// A prior copy can survive a crash before the journal records it.
		old, readErr := os.ReadFile(filepath.Join(root, "manifest.json"))
		if readErr != nil || string(old) != string(append(body, '\n')) {
			return errors.New("A different checkpoint already exists. Keep it for recovery inspection.")
		}
		if err := os.RemoveAll(stage); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if err := os.Rename(stage, root); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(root)); err != nil {
		return err
	}
	j.Checkpoint = checkpoint
	return nil
}

func RestoreUpdateCheckpoint(ctx context.Context, home string, j *UpdateJournal, run RuntimeDocker) error {
	run = updateOwnedDocker(j, run)
	if j.Committed() {
		return errors.New("This update committed. Repair the installed release with current data.")
	}
	if j.Checkpoint == nil {
		return errors.New("The update has no complete checkpoint. Retry the retained candidate.")
	}
	if run == nil {
		run = runRuntimeDocker
	}
	if err := verifyUpdateCheckpoint(ctx, home, j, run); err != nil {
		return err
	}
	// Every source remains intact while the destination paths exchange separately.
	for i, p := range j.Checkpoint.Paths {
		if err := validateUpdatePathParents(p.Path); err != nil {
			return err
		}
		parent := filepath.Dir(p.Path)
		if err := os.MkdirAll(parent, 0700); err != nil {
			return err
		}
		stage := filepath.Join(parent, ".culler-restore-"+j.ID+"-"+fmt.Sprint(i))
		retired := stage + ".old"
		if err := os.RemoveAll(stage); err != nil {
			return err
		}
		if p.Present {
			if err := copyUpdatePath(p.Backup, stage); err != nil {
				return err
			}
		}
		if err := os.RemoveAll(retired); err != nil {
			return err
		}
		if _, err := os.Lstat(p.Path); err == nil {
			if err := os.Rename(p.Path, retired); err != nil {
				return err
			}
			if err := syncDirectory(parent); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if p.Present {
			if err := os.Rename(stage, p.Path); err != nil {
				return err
			}
		}
		if err := syncDirectory(parent); err != nil {
			return err
		}
		if err := os.RemoveAll(retired); err != nil {
			return err
		}
	}
	if j.Previous != nil {
		for _, name := range j.Checkpoint.AbsentVolumes {
			if err := requireUpdateVolumeIdle(ctx, name, run); err != nil {
				return err
			}
			if err := removeStoppedUpdateVolumeUsers(ctx, name, j.Deployment, run); err != nil {
				return err
			}
			record, exists, err := inspectUpdateVolume(ctx, name, run)
			if err != nil {
				return err
			}
			if exists {
				if record.Labels["mc-deployment"] != j.Deployment {
					return errors.New("The restore destination has another owner.")
				}
				if _, err := run(ctx, "", "volume", "rm", name); err != nil {
					return err
				}
			}
		}
		for _, v := range j.Checkpoint.Volumes {
			if err := requireUpdateVolumeIdle(ctx, v.Name, run); err != nil {
				return err
			}
			if err := removeStoppedUpdateVolumeUsers(ctx, v.Name, j.Deployment, run); err != nil {
				return err
			}
			record, exists, err := inspectUpdateVolume(ctx, v.Name, run)
			_ = record
			if err != nil {
				return err
			}
			if exists {
				if _, err := run(ctx, "", "volume", "rm", v.Name); err != nil {
					return fmt.Errorf("Remove the stopped restore destination: %w", err)
				}
			}
			args := []string{"volume", "create"}
			keys := make([]string, 0, len(v.Labels))
			for key := range v.Labels {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				args = append(args, "--label", key+"="+v.Labels[key])
			}
			args = append(args, v.Name)
			if _, err := run(ctx, "", args...); err != nil {
				return err
			}
			if err := copyUpdateVolume(ctx, j.Previous.Image, v.Backup, v.Name, run); err != nil {
				return err
			}
			sum, err := updateVolumeDigest(ctx, j.Previous.Image, v.Name, run)
			if err != nil {
				return err
			}
			if sum != v.SHA256 {
				return errors.New("The restored volume failed its checksum.")
			}
		}
		if err := InstallRuntimeVolume(ctx, home, j.Previous.Image, j.Previous.Commit, run); err != nil {
			return err
		}
	}
	return nil
}

func CleanupUpdateCheckpoint(ctx context.Context, home string, j *UpdateJournal, run RuntimeDocker) error {
	if !j.Committed() && j.Phase != "restored" {
		return errors.New("An incomplete update still requires its recovery artifacts.")
	}
	if err := j.validate(home); err != nil {
		return err
	}
	if run == nil {
		run = runRuntimeDocker
	}
	if j.Checkpoint != nil {
		for _, v := range j.Checkpoint.Volumes {
			record, exists, err := inspectUpdateVolume(ctx, v.Backup, run)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			if record.Labels["mc-update"] != j.ID || record.Labels["mc-deployment"] != j.Deployment {
				return errors.New("The checkpoint volume belongs to another update.")
			}
			if _, err := run(ctx, "", "volume", "rm", v.Backup); err != nil {
				return fmt.Errorf("The checkpoint cleanup requires a retry: %w", err)
			}
		}
	}
	if j.Checkpoint != nil {
		if err := os.RemoveAll(j.Checkpoint.Root); err != nil {
			return err
		}
	}
	return syncDirectory(UpdateDirectory(home, j.ID))
}

// SyncUpdateDeployment orders installed bytes before the committed journal.
func SyncUpdateDeployment(ctx context.Context, home string, j *UpdateJournal, run RuntimeDocker) error {
	if err := j.validate(home); err != nil {
		return err
	}
	paths := updateHostPaths(home, j)
	if j.Candidate.HostBun != nil {
		paths = append(paths, filepath.Join(home, "bun", j.Candidate.HostBun.Version))
	}
	for _, root := range paths {
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			err = file.Sync()
			closeErr := file.Close()
			if err != nil {
				return err
			}
			return closeErr
		})
		if err != nil {
			return err
		}
		if err := syncDirectory(filepath.Dir(root)); err != nil {
			return err
		}
	}
	run = updateOwnedDocker(j, run)
	if _, err := run(ctx, "", "run", "--rm", operatorconfig.PidsLimitFlag, "--network", "none", "--user", "0:0", j.Candidate.Image, "/bin/sync"); err != nil {
		return fmt.Errorf("Synchronize the installed runtime: %w", err)
	}
	return nil
}

func verifyUpdateCheckpoint(ctx context.Context, home string, j *UpdateJournal, run RuntimeDocker) error {
	run = updateOwnedDocker(j, run)
	if j.Checkpoint == nil {
		return errors.New("The update checkpoint is absent.")
	}
	if err := j.Checkpoint.validate(home, j); err != nil {
		return err
	}
	if run == nil {
		run = runRuntimeDocker
	}
	for _, p := range j.Checkpoint.Paths {
		if !p.Present {
			continue
		}
		sum, _, err := updateTreeDigest(p.Backup)
		if err != nil {
			return err
		}
		if sum != p.SHA256 {
			return errors.New("The retained host checkpoint failed its checksum.")
		}
	}
	if j.Previous != nil {
		for _, v := range j.Checkpoint.Volumes {
			record, exists, err := inspectUpdateVolume(ctx, v.Backup, run)
			if err != nil {
				return err
			}
			if !exists || record.Labels["mc-update"] != j.ID || record.Labels["mc-deployment"] != j.Deployment {
				return errors.New("The retained volume checkpoint is absent or has a different owner.")
			}
			sum, err := updateVolumeDigest(ctx, j.Previous.Image, v.Backup, run)
			if err != nil {
				return err
			}
			if sum != v.SHA256 {
				return errors.New("The retained volume checkpoint failed its checksum.")
			}
		}
	}
	return nil
}

func updateOwnedDocker(j *UpdateJournal, run RuntimeDocker) RuntimeDocker {
	if run == nil {
		run = runRuntimeDocker
	}
	return func(ctx context.Context, input string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "run" {
			for _, arg := range args {
				if arg == "mc-update="+j.ID {
					return run(ctx, input, args...)
				}
			}
			sum := sha256.Sum256([]byte(strings.Join(args, "\x00")))
			name := "culler-update-" + j.ID + "-" + hex.EncodeToString(sum[:4])
			args = append([]string{"run", "--name", name, "--label", "mc-update=" + j.ID, "--label", "mc-deployment=" + j.Deployment}, args[1:]...)
		}
		return run(ctx, input, args...)
	}
}

type updateVolumeRecord struct {
	Name    string
	Driver  string
	Options map[string]string
	Labels  map[string]string
}

func inspectUpdateVolume(ctx context.Context, name string, run RuntimeDocker) (updateVolumeRecord, bool, error) {
	var record updateVolumeRecord
	body, err := run(ctx, "", "volume", "inspect", "--format", "{{json .}}", name)
	if err != nil {
		if strings.Contains(strings.ToLower(string(body)), "no such volume") {
			return record, false, nil
		}
		return record, false, fmt.Errorf("Inspect the update volume: %w", err)
	}
	if json.Unmarshal(body, &record) != nil || record.Name != name || record.Driver != "local" || len(record.Options) != 0 {
		return record, false, errors.New("The update volume has an invalid configuration.")
	}
	return record, true, nil
}
func requireUpdateVolumeIdle(ctx context.Context, name string, run RuntimeDocker) error {
	body, err := run(ctx, "", "container", "ls", "--quiet", "--filter", "volume="+name)
	if err != nil {
		return fmt.Errorf("Inspect volume writers: %w", err)
	}
	if strings.TrimSpace(string(body)) != "" {
		return errors.New("A container still uses the protected volume. Keep Culler stopped and retry.")
	}
	return nil
}
func ensureUpdateBackupVolume(ctx context.Context, name string, j *UpdateJournal, run RuntimeDocker) error {
	record, exists, err := inspectUpdateVolume(ctx, name, run)
	if err != nil {
		return err
	}
	if exists {
		if record.Labels["mc-update"] != j.ID || record.Labels["mc-deployment"] != j.Deployment {
			return errors.New("The checkpoint volume belongs to another update.")
		}
		if err := requireUpdateVolumeIdle(ctx, name, run); err != nil {
			return err
		}
		if _, err := run(ctx, "", "volume", "rm", name); err != nil {
			return err
		}
	}
	_, err = run(ctx, "", "volume", "create", "--label", "mc-update="+j.ID, "--label", "mc-deployment="+j.Deployment, name)
	return err
}
func copyUpdateVolume(ctx context.Context, image, source, target string, run RuntimeDocker) error {
	script := `set -eu; need=$(du -sk /source | cut -f1); free=$(df -Pk /destination | tail -n 1 | awk '{print $4}'); test "$free" -ge "$((need + 262144))"; cp -a /source/. /destination/; sync`
	out, err := run(ctx, "", "run", "--rm", operatorconfig.PidsLimitFlag, "--network", "none", "--user", "0:0", "--mount", "type=volume,src="+source+",dst=/source,readonly,volume-nocopy", "--mount", "type=volume,src="+target+",dst=/destination,volume-nocopy", image, "/bin/sh", "-ec", script)
	if err != nil {
		return fmt.Errorf("Copy the update volume: %w: %s", err, lastLine(out))
	}
	return nil
}

const updateVolumeHashScript = `const fs=require('fs'),p=require('path'),c=require('crypto'),h=c.createHash('sha256');function walk(root,rel){const f=p.join(root,rel),s=fs.lstatSync(f);h.update(JSON.stringify([rel,s.mode,s.uid,s.gid]));if(s.isDirectory()){for(const n of fs.readdirSync(f).sort())walk(root,p.join(rel,n));}else if(s.isSymbolicLink()){h.update(fs.readlinkSync(f));}else if(s.isFile()){const d=c.createHash('sha256'),fd=fs.openSync(f,'r'),b=Buffer.alloc(1048576);try{let n;while((n=fs.readSync(fd,b,0,b.length,null))>0)d.update(b.subarray(0,n));}finally{fs.closeSync(fd)}h.update(d.digest());}else throw Error('Unsupported checkpoint file');}walk('/source','');process.stdout.write(h.digest('hex'));`

func updateVolumeDigest(ctx context.Context, image, name string, run RuntimeDocker) (string, error) {
	body, err := run(ctx, "", "run", "--rm", operatorconfig.PidsLimitFlag, "--network", "none", "--user", "0:0", "--mount", "type=volume,src="+name+",dst=/source,readonly,volume-nocopy", image, RuntimeRoot+"/node/bin/node", "-e", updateVolumeHashScript)
	digest := strings.TrimSpace(string(body))
	if err != nil || !validSHA256(digest) {
		return "", errors.New("The volume checksum check failed.")
	}
	return digest, nil
}
func checkUpdateSpine(ctx context.Context, image, name, schema string, run RuntimeDocker) error {
	body, err := run(ctx, "", "run", "--rm", operatorconfig.PidsLimitFlag, "--network", "none", "--cap-drop", "ALL", "--user", "10002:10002", "--mount", "type=volume,src="+name+",dst=/mc/spine,readonly", image, "mc", "__release-database")
	var record struct {
		Schema string `json:"schema"`
		Empty  bool   `json:"empty"`
	}
	if err != nil || json.Unmarshal(body, &record) != nil || record.Schema != schema || record.Empty {
		return errors.New("The spine checkpoint failed its integrity or schema check.")
	}
	return nil
}

func updateDiskSpace(path string, bytes int64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return err
	}
	if bytes < 0 || uint64(stat.Bavail)*uint64(stat.Bsize) < uint64(bytes) {
		return errors.New("The update checkpoint needs more free disk space. Free space and retry.")
	}
	return nil
}
func copyUpdatePath(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(source)
		if err != nil {
			return err
		}
		return os.Symlink(link, target)
	case info.IsDir():
		if err := os.Mkdir(target, 0700); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyUpdatePath(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		if err := os.Chmod(target, info.Mode().Perm()); err != nil {
			return err
		}
		return syncDirectory(target)
	case info.Mode().IsRegular():
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		if err == nil {
			err = out.Chmod(info.Mode().Perm())
		}
		if err == nil {
			err = out.Sync()
		}
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		return err
	default:
		return errors.New("The update checkpoint contains an unsupported file type.")
	}
}
func updateTreeDigest(root string) (string, int64, error) {
	hash := sha256.New()
	var size int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		meta, _ := json.Marshal([]any{rel, uint32(info.Mode())})
		hash.Write(meta)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hash.Write([]byte(link))
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("The update checkpoint contains an unsupported file type.")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		sum := sha256.New()
		n, err := io.Copy(sum, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		size += n
		hash.Write(sum.Sum(nil))
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func RemoveUpdateArtifacts(home string, j *UpdateJournal) error {
	if j.Phase != "complete" && j.Phase != "restored" {
		return errors.New("The update must complete before candidate cleanup.")
	}
	if err := j.validate(home); err != nil {
		return err
	}
	if err := os.RemoveAll(UpdateDirectory(home, j.ID)); err != nil {
		return err
	}
	return syncDirectory(filepath.Join(home, "updates"))
}

func validateUpdatePathParents(path string) error {
	for parent := filepath.Dir(path); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return errors.New("An update destination has a linked or invalid parent directory.")
		}
	}
	return nil
}
func removeStoppedUpdateVolumeUsers(ctx context.Context, volume, deployment string, run RuntimeDocker) error {
	body, err := run(ctx, "", "container", "ls", "--all", "--quiet", "--filter", "volume="+volume)
	if err != nil {
		return fmt.Errorf("Inspect stopped volume users: %w", err)
	}
	for _, id := range strings.Fields(string(body)) {
		if !regexpContainerID.MatchString(id) {
			return errors.New("A volume user has an invalid container identity.")
		}
		data, err := run(ctx, "", "container", "inspect", "--format", "{{json .}}", id)
		if err != nil {
			return err
		}
		var record struct {
			State  struct{ Running bool }
			Config struct{ Labels map[string]string }
		}
		if json.Unmarshal(data, &record) != nil || record.State.Running || record.Config.Labels["mc-deployment"] != deployment {
			return errors.New("A container prevents restoration of the protected volume.")
		}
		if _, err := run(ctx, "", "container", "rm", id); err != nil {
			return err
		}
	}
	return nil
}
