#!/bin/sh
set -eu
/opt/culler-runtime/bin/node - <<'JS'
const fs = require('node:fs');
const path = require('node:path');
const { createHash } = require('node:crypto');
const { execFileSync } = require('node:child_process');
const root = '/opt/culler-runtime';
const files = {};
function visit(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const file = path.join(dir, entry.name);
    if (file === root + '/manifest.json') continue;
    if (entry.isDirectory()) visit(file);
    else if (entry.isFile()) files[path.relative(root, file)] = createHash('sha256').update(fs.readFileSync(file)).digest('hex');
  }
}
visit(root);
const version = file => execFileSync(file, ['--version'], { encoding: 'utf8' }).trim();
const tools = { node: version(root + '/bin/node'), bun: version(root + '/bin/bun') };
function browsers(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) browsers(file);
    else if (entry.isFile() && entry.name === 'chrome') tools[file] = version(file);
  }
}
browsers(root + '/ms-playwright');
console.log(JSON.stringify({ format: 1, release: fs.readFileSync(root + '/release-id', 'utf8').trim(), tools, files }, null, 2));
JS
