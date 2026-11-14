// The runtime volume install. The runtime Node.js runs this program as root in
// a container of the product image. The program makes the volume equal to the
// image runtime, file for file, and writes only the entries that differ.
//
// Arguments: <image runtime> <volume> <release>.
//
// The install removes the volume release marker first and writes it last. An
// interrupted install therefore leaves no release marker, and the next install
// repairs the volume. The install writes each changed file as a new file and
// renames it over the old path. Some volume files share hard links, so a write
// in place would change every linked path.
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

const MANIFEST = 'manifest.json';
const RELEASE_ID = 'release-id';
const TEMPORARY_PREFIX = '.culler-install.';
const MODE_BITS = 0o7777;

const counts = { written: 0, unchanged: 0, hashed: 0, removed: 0 };

// The install fsyncs the written files and the changed directories in one
// pass after the walk. One journal commit then covers many files.
const pending = { files: [], directories: [] };

function fail(message) {
  process.stderr.write('runtime install: ' + message + '\n');
  process.exit(2);
}

function lstatOrNull(file) {
  try {
    return fs.lstatSync(file);
  } catch (error) {
    if (error.code === 'ENOENT' || error.code === 'ENOTDIR') return null;
    throw error;
  }
}

// tryReadText returns null for a file that is absent or unreadable.
function tryReadText(file) {
  try {
    return fs.readFileSync(file, 'utf8');
  } catch {
    return null;
  }
}

function readManifest(file) {
  const text = tryReadText(file);
  if (text === null) return null;
  let manifest;
  try {
    manifest = JSON.parse(text);
  } catch {
    return null;
  }
  if (!manifest || manifest.format !== 1 || typeof manifest.release !== 'string' ||
      !manifest.files || typeof manifest.files !== 'object') {
    return null;
  }
  return manifest;
}

function sha256(file) {
  const hash = crypto.createHash('sha256');
  const fd = fs.openSync(file, 'r');
  try {
    const buffer = Buffer.allocUnsafe(1 << 20);
    for (;;) {
      const length = fs.readSync(fd, buffer, 0, buffer.length, null);
      if (length === 0) break;
      hash.update(buffer.subarray(0, length));
    }
  } finally {
    fs.closeSync(fd);
  }
  return hash.digest('hex');
}

function fsyncFile(file) {
  const fd = fs.openSync(file, 'r');
  try {
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
}

function fsyncDirectory(dir) {
  // A directory fsync makes the renames of that directory durable.
  // Some filesystems refuse it; the install then relies on the file fsyncs.
  let fd;
  try {
    fd = fs.openSync(dir, 'r');
    fs.fsyncSync(fd);
  } catch {
    return;
  } finally {
    if (fd !== undefined) fs.closeSync(fd);
  }
}

function sameAttributes(imageStat, volumeStat) {
  return (imageStat.mode & MODE_BITS) === (volumeStat.mode & MODE_BITS) &&
    imageStat.uid === volumeStat.uid && imageStat.gid === volumeStat.gid;
}

function setAttributes(file, stat) {
  // A chown clears the setuid and setgid bits, so the chmod comes second.
  fs.chownSync(file, stat.uid, stat.gid);
  fs.chmodSync(file, stat.mode & MODE_BITS);
  const result = fs.lstatSync(file);
  if (!sameAttributes(stat, result)) {
    throw new Error(file + ' has mode ' + (result.mode & MODE_BITS).toString(8) + ' and owner ' +
      result.uid + ':' + result.gid + ' after the attribute change');
  }
}

// remove unlinks one entry. A directory goes with its content. The routine
// follows no symlink.
function remove(file) {
  const stat = lstatOrNull(file);
  if (!stat) return;
  if (stat.isDirectory()) {
    for (const name of fs.readdirSync(file)) remove(path.join(file, name));
    fs.rmdirSync(file);
  } else {
    fs.unlinkSync(file);
  }
}

function temporaryName(file) {
  return path.join(path.dirname(file), TEMPORARY_PREFIX + path.basename(file));
}

// writeNewFile places the content as a new file and renames it over the path.
function writeNewFile(volumeFile, stat, fill) {
  const temporary = temporaryName(volumeFile);
  remove(temporary);
  fill(temporary);
  setAttributes(temporary, stat);
  fs.renameSync(temporary, volumeFile);
  pending.files.push(volumeFile);
}

function copyNewFile(imageFile, volumeFile, stat) {
  writeNewFile(volumeFile, stat, temporary => fs.copyFileSync(imageFile, temporary));
}

// syncFile keeps a volume file with the same hash and attributes as the image
// file. Every other state receives a new copy of the image file.
function syncFile(relative, imageFile, volumeFile, imageStat, volumeStat, newManifest, oldManifest) {
  const newHash = newManifest.files[relative];
  if (typeof newHash !== 'string') throw new Error('the image manifest lacks ' + relative);
  if (volumeStat && !volumeStat.isFile()) {
    remove(volumeFile);
    volumeStat = null;
  }
  if (volumeStat) {
    let oldHash = oldManifest ? oldManifest.files[relative] : undefined;
    if (typeof oldHash !== 'string') {
      oldHash = sha256(volumeFile);
      counts.hashed++;
    }
    if (oldHash === newHash && sameAttributes(imageStat, volumeStat)) {
      counts.unchanged++;
      return false;
    }
  }
  copyNewFile(imageFile, volumeFile, imageStat);
  counts.written++;
  return true;
}

function syncSymlink(imageFile, volumeFile, imageStat, volumeStat) {
  const target = fs.readlinkSync(imageFile);
  if (volumeStat && volumeStat.isSymbolicLink() && fs.readlinkSync(volumeFile) === target) {
    if (volumeStat.uid === imageStat.uid && volumeStat.gid === imageStat.gid) return false;
    fs.lchownSync(volumeFile, imageStat.uid, imageStat.gid);
    return true;
  }
  if (volumeStat) remove(volumeFile);
  fs.symlinkSync(target, volumeFile);
  fs.lchownSync(volumeFile, imageStat.uid, imageStat.gid);
  return true;
}

// syncDirectory makes one volume directory equal to one image directory.
// The root skips the manifest and the release marker; the main program writes
// those two files after every other entry.
function syncDirectory(relative, imageDir, volumeDir, imageStat, newManifest, oldManifest) {
  let volumeStat = lstatOrNull(volumeDir);
  let changed = false;
  if (volumeStat && !volumeStat.isDirectory()) {
    remove(volumeDir);
    volumeStat = null;
  }
  if (!volumeStat) {
    fs.mkdirSync(volumeDir);
    changed = true;
  }
  const reserved = relative === '' ? new Set([MANIFEST, RELEASE_ID]) : new Set();
  const names = new Set();
  for (const name of fs.readdirSync(imageDir)) {
    if (reserved.has(name)) continue;
    names.add(name);
    const imageFile = path.join(imageDir, name);
    const volumeFile = path.join(volumeDir, name);
    const entryRelative = relative === '' ? name : relative + '/' + name;
    const entryStat = fs.lstatSync(imageFile);
    const existing = lstatOrNull(volumeFile);
    if (entryStat.isDirectory()) {
      if (syncDirectory(entryRelative, imageFile, volumeFile, entryStat, newManifest, oldManifest)) changed = true;
    } else if (entryStat.isSymbolicLink()) {
      if (syncSymlink(imageFile, volumeFile, entryStat, existing)) changed = true;
    } else if (entryStat.isFile()) {
      if (syncFile(entryRelative, imageFile, volumeFile, entryStat, existing, newManifest, oldManifest)) changed = true;
    } else {
      throw new Error('the image runtime holds an unsupported entry at ' + entryRelative);
    }
  }
  for (const name of fs.readdirSync(volumeDir)) {
    if (names.has(name) || reserved.has(name)) continue;
    remove(path.join(volumeDir, name));
    counts.removed++;
    changed = true;
  }
  // The attributes come last, so a read-only directory mode never blocks the
  // writes of its entries.
  if (!sameAttributes(imageStat, fs.lstatSync(volumeDir))) {
    setAttributes(volumeDir, imageStat);
    changed = true;
  }
  if (changed) pending.directories.push(volumeDir);
  return changed;
}

// flush makes the pending writes durable: the content of each written file
// first, then the renames and removals of each changed directory.
function flush() {
  for (const file of pending.files) fsyncFile(file);
  for (const dir of pending.directories) fsyncDirectory(dir);
  pending.files = [];
  pending.directories = [];
}

function syncManifest(imageRoot, volumeRoot) {
  const imageFile = path.join(imageRoot, MANIFEST);
  const volumeFile = path.join(volumeRoot, MANIFEST);
  const imageStat = fs.lstatSync(imageFile);
  const volumeStat = lstatOrNull(volumeFile);
  if (volumeStat && volumeStat.isFile() && sameAttributes(imageStat, volumeStat) &&
      fs.readFileSync(imageFile).equals(fs.readFileSync(volumeFile))) {
    return;
  }
  if (volumeStat && !volumeStat.isFile()) remove(volumeFile);
  copyNewFile(imageFile, volumeFile, imageStat);
}

function writeReleaseId(imageRoot, volumeRoot, release) {
  const imageStat = fs.lstatSync(path.join(imageRoot, RELEASE_ID));
  const volumeFile = path.join(volumeRoot, RELEASE_ID);
  if (lstatOrNull(volumeFile)) remove(volumeFile);
  writeNewFile(volumeFile, imageStat, temporary => fs.writeFileSync(temporary, release + '\n'));
}

function main() {
  const [imageRoot, volumeRoot, release] = process.argv.slice(2);
  if (!imageRoot || !volumeRoot || !release || process.argv.length !== 5) {
    fail('usage: node - <image runtime> <volume> <release>');
  }
  const imageRelease = tryReadText(path.join(imageRoot, RELEASE_ID));
  if (imageRelease === null || imageRelease.trim() !== release) {
    fail('the image runtime at ' + imageRoot + ' is not release ' + release);
  }
  const newManifest = readManifest(path.join(imageRoot, MANIFEST));
  if (!newManifest || newManifest.release !== release) {
    fail('the image runtime at ' + imageRoot + ' has no manifest for release ' + release);
  }
  const imageStat = lstatOrNull(imageRoot);
  const volumeStat = lstatOrNull(volumeRoot);
  if (!imageStat || !imageStat.isDirectory() || !volumeStat || !volumeStat.isDirectory()) {
    fail('the image runtime and the volume must be directories');
  }

  // The volume manifest describes the volume content only when the last
  // install completed. The release marker of a complete install names the
  // same release as its manifest. Without that marker, the install hashes
  // each volume file. The program runs for the release of the volume only
  // after the release check failed. That run is a repair and hashes each
  // volume file.
  let oldManifest = readManifest(path.join(volumeRoot, MANIFEST));
  const oldRelease = tryReadText(path.join(volumeRoot, RELEASE_ID));
  if (oldManifest && (oldRelease === null || oldRelease.trim() !== oldManifest.release || oldRelease.trim() === release)) {
    oldManifest = null;
  }

  remove(path.join(volumeRoot, RELEASE_ID));
  fsyncDirectory(volumeRoot);
  syncDirectory('', imageRoot, volumeRoot, imageStat, newManifest, oldManifest);
  flush();
  syncManifest(imageRoot, volumeRoot);
  flush();
  fsyncDirectory(volumeRoot);
  writeReleaseId(imageRoot, volumeRoot, release);
  flush();
  fsyncDirectory(volumeRoot);
  process.stdout.write(JSON.stringify({ release, ...counts }) + '\n');
}

try {
  main();
} catch (error) {
  process.stderr.write('runtime install: ' + (error && error.message ? error.message : String(error)) + '\n');
  process.exit(1);
}
