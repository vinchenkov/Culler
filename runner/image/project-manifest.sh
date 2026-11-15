#!/bin/sh
set -eu
node - <<'JS'
const { execFileSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { readFileSync, realpathSync, readdirSync } = require('node:fs');
const run = (cmd, args) => execFileSync(cmd, args, { encoding: 'utf8' }).trim();
const binaries = ['node', 'npm', 'bun', 'go', 'uv', 'mise', 'python3', 'pnpm', 'git', 'cmake', 'ninja', 'cc', 'c++', 'make', 'pkg-config', 'rg', 'jq', 'playwright-cli'];
const tools = Object.fromEntries(binaries.map(name => {
  const args = name === 'go' ? ['version'] : ['--version'];
  return [name, run(name, args).split('\n')[0]];
}));
const sha256 = file => createHash('sha256').update(readFileSync(file)).digest('hex');
const executable_sha256 = Object.fromEntries(binaries.map(name => {
  const file = realpathSync(run('/bin/sh', ['-c', 'command -v ' + name]));
  return [name, sha256(file)];
}));
const artifacts = Object.fromEntries(['NODE', 'BUN', 'GO', 'UV', 'MISE', 'PYTHON'].map(name => [name.toLowerCase(), {
  version: process.env[name + '_VERSION'], sha256: process.env[name + '_LINUX_ARM64_SHA256'],
}]));
const browser_versions = {};
function browsers(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const file = dir + '/' + entry.name;
    if (entry.isDirectory()) browsers(file);
    else if (entry.isFile() && ['chrome', 'headless_shell', 'chrome-headless-shell'].includes(entry.name)) {
      browser_versions[file] = run(file, ['--version']);
    }
  }
}
browsers('/ms-playwright');
console.log(JSON.stringify({ format: 1, platform: 'linux/arm64', tools, executable_sha256, artifacts,
  ubuntu_snapshot: process.env.UBUNTU_SNAPSHOT,
  browser_artifacts: readdirSync('/ms-playwright').filter(name => !name.startsWith('.')), browser_versions,
  packages_sha256: sha256('/usr/share/culler-project/system-packages.tsv'),
  package_lock_sha256: sha256('/opt/project-tools/package-lock.json') }, null, 2));
JS
