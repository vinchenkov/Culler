package deployment

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpenCodeSourceProgramVerifiesArchiveContentsAndNativeVersion(t *testing.T) {
	program := filepath.Join(t.TempDir(), "source.py")
	if err := os.WriteFile(program, []byte(openCodeSourceProgram), 0600); err != nil {
		t.Fatal(err)
	}
	// Exercise the same archive and volume readers on actual files, links, modes, and native replies.
	script := `import json, os, pathlib, runpy, subprocess, sys, tarfile, tempfile
module = runpy.run_path(sys.argv[1])
with tempfile.TemporaryDirectory() as temporary:
    root = pathlib.Path(temporary) / 'volume'
    root.mkdir()
    binary = root / 'node_modules/@opencode/cli-linux-arm64/bin/opencode'
    binary.parent.mkdir(parents=True)
    binary.write_text('#!/bin/sh\nprintf "opencode v2.2.5\\n"\n')
    binary.chmod(0o755)
    plugin = root / 'plugin'
    plugin.write_text('original plugin')
    plugin.chmod(0o644)
    (root / 'link').symlink_to('plugin')
    archive = pathlib.Path(temporary) / 'archive.tar.gz'
    with tarfile.open(archive, 'w:gz') as output:
        output.add(root, arcname='.')
    expected = module['archive_digest'](archive)
    assert expected == module['volume_digest'](root)
    (root / 'release-id').write_text('release\n')
    (root / 'archive-sha256').write_text('archive\n')
    def verify(version='2.2.5', digest=expected):
        return subprocess.run([sys.executable, sys.argv[1], 'verify', str(root), digest, 'archive', 'release', version], capture_output=True, text=True)
    assert verify().returncode == 0
    assert verify('2.2.6').returncode != 0
    binary.write_text('#!/bin/sh\nprintf "opencode v2.2.6\\n"\n')
    assert verify().returncode != 0
    changed = module['volume_digest'](root)
    assert verify('2.2.6', changed).returncode == 0
    assert verify('2.2.5', changed).returncode != 0
    binary.write_text('#!/bin/sh\nprintf "opencode v2.2.5\\n"\n')
    plugin.write_text('damaged plugin')
    assert verify().returncode != 0
    plugin.write_text('original plugin')
    plugin.chmod(0o600)
    assert verify().returncode != 0
    plugin.chmod(0o644)
    (root / 'link').unlink()
    (root / 'link').symlink_to('node_modules/@opencode/cli-linux-arm64/bin/opencode')
    assert verify().returncode != 0
    (root / 'link').unlink()
    (root / 'link').symlink_to('plugin')
    (root / 'unexpected').write_text('extra')
    assert verify().returncode != 0
    (root / 'unexpected').unlink()
    (root / 'release-id').unlink()
    assert verify().returncode != 0
    (root / 'release-id').write_text('release\n')
    plugin.unlink()
    assert verify().returncode != 0
`
	output, err := exec.Command("python3", "-c", script, program).CombinedOutput()
	if err != nil {
		t.Fatalf("The OpenCode content checks failed: %v: %s", err, output)
	}
}
