// This file is in the internal package (not substrate_test) on purpose: the
// mountinfo parse and the accept/refuse decision are pure and platform-neutral,
// so they are proven on every host, while only the /proc/self/mountinfo read
// behind them is Linux-only.
package substrate

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mountinfo fixtures are the shapes observed on Docker Desktop.
const (
	// A named volume inside the Docker Desktop VM: block-device-backed ext4.
	mountNamedVolume = `1234 1000 254:1 /var/lib/docker/volumes/mc-spine/_data /mc/spine rw,relatime - ext4 /dev/vda1 rw`
	// The container rootfs.
	mountOverlayRoot = `1000 999 0:50 / / rw,relatime - overlay overlay rw,lowerdir=/x`
	// The host bind as Docker Desktop actually surfaces it: NOT "virtiofs" but
	// "fakeowner", its FUSE ownership shim. A denylist keyed on "virtiofs"
	// would have accepted this, which is why the allowlist shape is the
	// load-bearing one.
	mountFakeownerBind = `1500 1000 0:77 /Users/op/dev /mc/spine rw,relatime - fakeowner /run/host_mark/Users rw`
)

func mountinfo(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestLockDomainAcceptsOnlyBlockDeviceBackedLocalFilesystems(t *testing.T) {
	for _, tc := range []struct {
		name    string
		info    string
		dir     string
		wantErr string // "" means the directory is inside the lock domain
	}{
		{
			name: "named volume is the lock domain",
			info: mountinfo(mountOverlayRoot, mountNamedVolume),
			dir:  "/mc/spine",
		},
		{
			name: "a subdirectory of the named volume rides its mount",
			info: mountinfo(mountOverlayRoot, mountNamedVolume),
			dir:  "/mc/spine/nested",
		},
		{
			name:    "Docker Desktop host bind is refused despite not being named virtiofs",
			info:    mountinfo(mountOverlayRoot, mountFakeownerBind),
			dir:     "/mc/spine",
			wantErr: "fakeowner",
		},
		{
			name:    "container rootfs is refused",
			info:    mountinfo(mountOverlayRoot),
			dir:     "/tmp",
			wantErr: "overlay",
		},
		{
			name:    "longest-prefix wins, so a bind nested under an allowed mount still loses",
			info:    mountinfo(mountOverlayRoot, mountNamedVolume, `1600 1234 0:78 / /mc/spine/bound rw - fakeowner /run/host_mark/Users rw`),
			dir:     "/mc/spine/bound",
			wantErr: "fakeowner",
		},
		{
			name:    "a prefix that is not a whole path component does not match",
			info:    mountinfo(mountOverlayRoot, mountNamedVolume),
			dir:     "/mc/spineless",
			wantErr: "overlay",
		},
		{
			name: "octal-escaped mount points are decoded before matching",
			info: mountinfo(mountOverlayRoot, `1234 1000 254:1 / /mc/my\040spine rw - ext4 /dev/vda1 rw`),
			dir:  "/mc/my spine",
		},
		{
			// Fail-closed: an allowed fstype without a block device behind it
			// is a bind of somebody else's ext4, not a runtime-local volume.
			name:    "an allowed fstype backed by a non-device source is refused",
			info:    mountinfo(mountOverlayRoot, `1234 1000 254:1 / /mc/spine rw - ext4 hostshare rw`),
			dir:     "/mc/spine",
			wantErr: "hostshare",
		},
		{
			name:    "an unknown filesystem is refused rather than assumed safe",
			info:    mountinfo(mountOverlayRoot, `1234 1000 0:90 / /mc/spine rw - 9p hostshare rw`),
			dir:     "/mc/spine",
			wantErr: "9p",
		},
		{
			name:    "no containing mount refuses rather than defaulting to accept",
			info:    mountinfo(mountNamedVolume),
			dir:     "/elsewhere",
			wantErr: "no mount",
		},
		{
			name:    "unparseable mountinfo refuses",
			info:    "garbage without a separator\n",
			dir:     "/mc/spine",
			wantErr: "unparseable",
		},
		{
			// Skipping a line we cannot read would silently promote the parent
			// mount, and on a Linux host that parent is an ext4 root that
			// PASSES — the guard would approve the very mount it failed to
			// parse. One bad line poisons the whole table.
			name:    "one unreadable line refuses even when a valid parent would pass",
			info:    mountinfo(`1 0 254:1 / / rw - ext4 /dev/vda1 rw`, `truncated /mc/spine line`),
			dir:     "/mc/spine",
			wantErr: "unparseable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkLockDomain(strings.NewReader(tc.info), tc.dir)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("checkLockDomain(%s) = %v, want accept", tc.dir, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkLockDomain(%s) = accept, want refusal naming %q", tc.dir, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("checkLockDomain(%s) = %v, want refusal naming %q", tc.dir, err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "runtime-local named volume") {
				t.Fatalf("refusal %v does not name the required volume", err)
			}
		})
	}
}

// Mounting twice at the same point SHADOWS rather than replaces: both lines
// stay in mountinfo, and the later one is what the process actually reads and
// writes. A guard that broke the length tie toward the first entry would judge
// a bind stacked over a named volume on the volume's ext4 line.
func TestLockDomainJudgesAShadowedMountPointOnTheEffectiveMount(t *testing.T) {
	shadowed := mountinfo(mountOverlayRoot, mountNamedVolume, mountFakeownerBind)
	if err := checkLockDomain(strings.NewReader(shadowed), "/mc/spine"); err == nil || !strings.Contains(err.Error(), "fakeowner") {
		t.Fatalf("bind stacked OVER the volume = %v, want the later mount refused", err)
	}
	// And the converse, so this is a tie-break rule and not a bias toward
	// refusal: a volume mounted over a bind is in the lock domain.
	restacked := mountinfo(mountOverlayRoot, mountFakeownerBind, mountNamedVolume)
	if err := checkLockDomain(strings.NewReader(restacked), "/mc/spine"); err != nil {
		t.Fatalf("volume stacked OVER the bind = %v, want accept", err)
	}
}

// The directory is not the whole story. Docker will bind a single file over
// spine.db inside an otherwise-legitimate named volume, which leaves the
// directory reporting ext4 while the database itself sits on VirtioFS — the
// corruption this guard exists to prevent, wearing a passing directory.
func TestLockDomainRefusesASingleFileBindInsideAnAllowedVolume(t *testing.T) {
	info := mountinfo(mountOverlayRoot, mountNamedVolume,
		`1700 1234 0:79 /Users/op/spine.db /mc/spine/spine.db rw - fakeowner /run/host_mark/Users rw`)

	if err := checkLockDomain(strings.NewReader(info), "/mc/spine"); err != nil {
		t.Fatalf("the volume DIRECTORY alone = %v; this fixture is only meaningful if the directory passes", err)
	}
	err := checkLockDomain(strings.NewReader(info), "/mc/spine", "/mc/spine/spine.db")
	if err == nil || !strings.Contains(err.Error(), "fakeowner") {
		t.Fatalf("checkLockDomain(dir+file) = %v, want the single-file bind refused", err)
	}
}

// A spine that does not exist yet has no mount line of its own; it rides its
// directory's, which is checked first. Absence must not read as a refusal, or
// provisioning a fresh spine becomes impossible.
func TestLockDomainAdmitsANotYetCreatedSpineInsideAnAllowedVolume(t *testing.T) {
	info := mountinfo(mountOverlayRoot, mountNamedVolume)
	if err := checkLockDomain(strings.NewReader(info), "/mc/spine", "/mc/spine/spine.db"); err != nil {
		t.Fatalf("checkLockDomain(fresh spine in the lock domain) = %v, want accept", err)
	}
}

// A read that fails is not a pass. The guard has no /proc to consult only on a
// platform where mc never opens the spine at all, and that exemption is made at
// the call site (GuardLockDomain), never by swallowing a read error here.
func TestLockDomainRefusesWhenMountinfoCannotBeRead(t *testing.T) {
	err := checkLockDomain(errReader{}, "/mc/spine")
	if err == nil || !strings.Contains(err.Error(), "runtime-local named volume") {
		t.Fatalf("checkLockDomain(unreadable) = %v, want a refusal that names the required volume", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("mountinfo unavailable") }

// ── Wiring ───────────────────────────────────────────────────────────────
//
// The tests above prove the DECISION. These prove the decision is actually
// CONSULTED, which is a separate claim and the one most at risk: the fast lane
// runs on darwin, where platformMountinfo is inert, so deleting the
// GuardLockDomain call from substrate.Open would turn nothing red. That is
// exactly how the verdict fence rotted — every test asserted the refusal
// path and none asserted the admission path, so a fence that had stopped
// working kept passing (6657541).

// withMountinfo points the guard at a fixed mount table for the duration of a
// test, standing in for the /proc the fast lane's platform does not have.
func withMountinfo(t *testing.T, info string) {
	t.Helper()
	prev := openMountinfo
	openMountinfo = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(info)), nil
	}
	t.Cleanup(func() { openMountinfo = prev })
}

// requireGuardedTempDir gives a test directory that the real guard admits.
// A production Linux build refuses a test directory outside a block-device
// filesystem, such as the overlay root of a container.
func requireGuardedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := GuardLockDomain(filepath.Join(dir, "control.db")); err != nil {
		t.Skipf("This test requires a test directory on a block-device filesystem: %v", err)
	}
	return dir
}

func TestOpenRefusesASpineOutsideTheLockDomain(t *testing.T) {
	dir := requireGuardedTempDir(t)

	// Sanity, on a path this test then abandons: with no hostile mount table
	// the open succeeds, so the refusal below is attributable to the guard and
	// not to something about temp directories.
	db, err := Open(filepath.Join(dir, "control.db"))
	if err != nil {
		t.Fatalf("Open = %v, want success before the mount table says otherwise", err)
	}
	db.Close()

	// Name the real directory, so the refusal is the fakeowner verdict rather
	// than an incidental "no mount contains it".
	withMountinfo(t, mountinfo(mountOverlayRoot,
		`1500 1000 0:77 /Users/op `+dir+` rw - fakeowner /run/host_mark/Users rw`))

	spine := filepath.Join(dir, "spine.db")
	if _, err := Open(spine); err == nil || !strings.Contains(err.Error(), "fakeowner") {
		t.Fatalf("Open(%s) = %v, want the lock-domain guard to refuse before sql.Open", spine, err)
	}
	// Refusing BEFORE sql.Open is the point: no database file may appear.
	if _, statErr := os.Stat(spine); !os.IsNotExist(statErr) {
		t.Fatalf("refused Open still created %s (stat err %v)", spine, statErr)
	}
}

// OpenReadOnly is the read verbs' opener, and a read-only open is no less
// bound by the lock doculler: the -shm sidecar a WAL read may attach to is
// lock-bearing, so consulting the guard is proven for this opener too, not
// inherited on faith from Open.
func TestOpenReadOnlyRefusesASpineOutsideTheLockDomain(t *testing.T) {
	dir := requireGuardedTempDir(t)
	spine := filepath.Join(dir, "spine.db")

	// Sanity: with no hostile mount table the open succeeds, so the refusal
	// below is attributable to the guard and not to the read-only DSN.
	db, err := Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err = OpenReadOnly(spine); err != nil {
		t.Fatalf("OpenReadOnly = %v, want success before the mount table says otherwise", err)
	}
	db.Close()

	withMountinfo(t, mountinfo(mountOverlayRoot,
		`1500 1000 0:77 /Users/op `+dir+` rw - fakeowner /run/host_mark/Users rw`))
	if _, err := OpenReadOnly(spine); err == nil || !strings.Contains(err.Error(), "fakeowner") {
		t.Fatalf("OpenReadOnly(%s) = %v, want the lock-domain guard to refuse before sql.Open", spine, err)
	}
}

// A spine file that is itself a symlink into a bind must be judged on the
// bind, not on the blameless directory holding the link.
func TestGuardResolvesTheSpineFileNotJustItsDirectory(t *testing.T) {
	volume, bind := t.TempDir(), t.TempDir()
	target := filepath.Join(bind, "real.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(volume, "spine.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolvedVolume, err := filepath.EvalSymlinks(volume)
	if err != nil {
		t.Fatal(err)
	}
	resolvedBind, err := filepath.EvalSymlinks(bind)
	if err != nil {
		t.Fatal(err)
	}
	// The directory holding the link is a legitimate volume; the link's target
	// is not. A guard that rejoined the basename to the resolved directory
	// would see only the first line and accept.
	withMountinfo(t, mountinfo(mountOverlayRoot,
		`1234 1000 254:1 / `+resolvedVolume+` rw - ext4 /dev/vda1 rw`,
		`1500 1000 0:77 /Users/op `+resolvedBind+` rw - fakeowner /run/host_mark/Users rw`))
	if err := GuardLockDomain(link); err == nil || !strings.Contains(err.Error(), "fakeowner") {
		t.Fatalf("GuardLockDomain(symlink into a bind) = %v, want the target's mount refused", err)
	}
}
