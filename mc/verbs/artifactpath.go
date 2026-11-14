package verbs

import (
	"os"
	"path/filepath"
	"strings"

	"mc/boundary"
)

// ResolveArtifactHostPath maps a container artifact-plane path recorded in the
// spine back to the host path its bytes live at.
//
// An agent writes every durable artifact inside its container, so the spine
// records a container path. No host process can read one by that name: the
// resident and the dashboard never see the container's namespace, and the warm
// helper mounts the spine volume alone.
//
// The mount allowlist is the reverse map, and the only honest one. It is the
// same authority dispatch authorizes real binds against, so a target it does
// not carry is a target no container was ever given. Any other derivation
// answers for plane bytes that were never bound.
//
// Host scope only. The container root comes from ArtifactsMountRoot, the same
// function the packaged terminal validates against, so both ends of the round
// trip agree by construction.
func ResolveArtifactHostPath(home, containerPath string) (string, error) {
	root := ArtifactsMountRoot()
	clean := filepath.Clean(containerPath)
	if !filepath.IsAbs(clean) || !strings.HasPrefix(clean, root+"/") {
		return "", Domainf("%q is not a container artifact-plane path under %s", containerPath, root)
	}
	rest := strings.TrimPrefix(clean, root+"/")
	target, suffix, found := strings.Cut(rest, "/")
	if !found || target == "" || suffix == "" {
		return "", Domainf("%q names no artifact target and file beneath %s", containerPath, root)
	}

	roots, err := ArtifactHostRoots(home)
	if err != nil {
		return "", err
	}
	hostRoot := roots[target]
	if hostRoot == "" {
		return "", Domainf("no mount allowlist entry carries artifact target %q, so no container was ever given that plane", target)
	}

	// Resolve BOTH sides through symlinks before comparing, then require
	// containment. An agent holds the artifact plane read-write, so it can
	// plant a symlink there pointing anywhere on the host; without this the
	// dashboard would serve whatever it points at. Resolving only the root
	// would leave the escape open, and resolving only the leaf would reject
	// every deployment whose workspace sits under a symlinked home.
	canonicalRoot, err := filepath.EvalSymlinks(hostRoot)
	if err != nil {
		return "", Domainf("artifact root %s for target %q is unreadable: %v", hostRoot, target, err)
	}
	hostPath := filepath.Join(canonicalRoot, filepath.FromSlash(suffix))
	canonicalPath, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		return "", Domainf("artifact %q does not exist on the host at %s: %v", containerPath, hostPath, err)
	}
	if canonicalPath != canonicalRoot && !strings.HasPrefix(canonicalPath, canonicalRoot+string(filepath.Separator)) {
		return "", Domainf("artifact %q resolves to %s, outside its own plane %s", containerPath, canonicalPath, canonicalRoot)
	}
	info, err := os.Stat(canonicalPath)
	if err != nil {
		return "", Domainf("stat artifact %s: %v", canonicalPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", Domainf("artifact %q is not a regular file", containerPath)
	}
	return canonicalPath, nil
}

// ArtifactHostRoots returns the allowlist's artifact-target to host-root map.
//
// A reader needs it to translate the container paths a packet render names,
// not just the render's own path. The Packager writes its packet inside a
// container and names the Verifier's evidence by the only path it can see. The
// render is the role's prose, so nothing rewrites it at rest; the surface that
// displays it translates as it draws.
//
// Host scope only, and the same authority as ResolveArtifactHostPath.
func ArtifactHostRoots(home string) (map[string]string, error) {
	allowlistPath := MountAllowlistPath(home)
	if err := boundary.TrustPolicyFile(allowlistPath, os.Getuid()); err != nil {
		return nil, Domainf("mount allowlist %s is not trusted: %v", allowlistPath, err)
	}
	data, err := os.ReadFile(allowlistPath)
	if err != nil {
		return nil, Domainf("read mount allowlist %s: %v", allowlistPath, err)
	}
	allowlist, err := boundary.ParseMountAllowlist(data)
	if err != nil {
		return nil, Domainf("parse mount allowlist %s: %v", allowlistPath, err)
	}
	roots := make(map[string]string, len(allowlist.Entries()))
	for _, entry := range allowlist.Entries() {
		roots[entry.Target] = entry.Path
	}
	return roots, nil
}
