#!/usr/bin/env python3
"""Read a Docker image archive from `docker image save`."""

import gzip
import hashlib
import io
import json
import struct
import tarfile
import zlib
from concurrent.futures import ThreadPoolExecutor

CHUNK = 1 << 20
ELF_MAGIC = b"\x7fELF"
GZIP_MAGIC = b"\x1f\x8b"


def normalize(name):
    while name.startswith("./"):
        name = name[2:]
    return name.strip("/")


class Entry:
    """One tar member of one layer."""

    __slots__ = ("path", "kind", "size", "mode", "uid", "gid", "linkname", "sha256", "sections")

    def __init__(self, member):
        self.path = normalize(member.name)
        if member.isfile():
            self.kind = "file"
        elif member.isdir():
            self.kind = "dir"
        elif member.issym():
            self.kind = "symlink"
        elif member.islnk():
            self.kind = "link"
        else:
            self.kind = "other"
        self.size = member.size
        self.mode = member.mode & 0o7777
        self.uid = member.uid
        self.gid = member.gid
        self.linkname = normalize(member.linkname) if member.linkname else ""
        self.sha256 = None
        self.sections = None


class Layer:
    """One layer of an image: its tar members, its whiteouts, and its sizes."""

    def __init__(self, index, diff_id, member, stored_size):
        self.index = index
        self.diff_id = diff_id
        self.member = member
        self.stored_size = stored_size
        self.compressed = False
        self.size = stored_size
        self.entries = {}
        self.deleted = []
        self.opaque = []

    def resolve(self, entry):
        """Follow a hard link to its regular file in this layer."""
        seen = set()
        while entry is not None and entry.kind == "link" and entry.linkname not in seen:
            seen.add(entry.linkname)
            entry = self.entries.get(entry.linkname)
        return entry


class Image:
    """The layers, the configuration, and the merged file view of one image archive."""

    def __init__(self, path, manifest, config, layers):
        self.path = path
        self.manifest = manifest
        self.config = config
        self.layers = layers
        self.diff_ids = [layer.diff_id for layer in layers]

    def history(self):
        return [entry.get("created_by", "") for entry in self.config.get("history", [])]

    def merged(self):
        """Return the final file view as a map from path to (layer index, entry)."""
        view = {}
        for layer in self.layers:
            for prefix in layer.opaque:
                view = {path: value for path, value in view.items() if not path.startswith(prefix)}
            for path in layer.deleted:
                view.pop(path, None)
                view = {key: value for key, value in view.items() if not key.startswith(path + "/")}
            for path, entry in layer.entries.items():
                view[path] = (layer.index, entry)
        return view


def elf_section_names(stream):
    """Return the section names of an ELF file, or None when the stream holds no ELF file."""
    header = stream.read(64)
    if len(header) < 64 or header[:4] != ELF_MAGIC:
        return None
    order = "<" if header[5] == 1 else ">"
    if header[4] == 2:
        (offset,) = struct.unpack_from(order + "Q", header, 0x28)
        entry_size, count, names_index = struct.unpack_from(order + "HHH", header, 0x3A)
        layout = order + "IIQQQQIIQQ"
    elif header[4] == 1:
        (offset,) = struct.unpack_from(order + "I", header, 0x20)
        entry_size, count, names_index = struct.unpack_from(order + "HHH", header, 0x2E)
        layout = order + "IIIIIIIIII"
    else:
        raise ValueError("The ELF class is unknown.")
    if offset == 0 or entry_size < struct.calcsize(layout):
        return []
    stream.seek(offset)
    first = struct.unpack_from(layout, stream.read(entry_size), 0)
    if count == 0:
        count = first[5]
    if names_index == 0xFFFF:
        names_index = first[6]
    stream.seek(offset)
    table = stream.read(entry_size * count)
    if len(table) < entry_size * count or names_index >= count:
        return []
    headers = [struct.unpack_from(layout, table, index * entry_size) for index in range(count)]
    stream.seek(headers[names_index][4])
    names = stream.read(headers[names_index][5]) + b"\0"
    result = []
    for header in headers[1:]:
        start = min(header[0], len(names) - 1)
        result.append(names[start:names.index(b"\0", start)].decode("ascii", "replace"))
    return result


def digest(stream):
    value = hashlib.sha256()
    while True:
        block = stream.read(CHUNK)
        if not block:
            return value.hexdigest()
        value.update(block)


class CountingReader:
    """Count the bytes that a stream yields."""

    def __init__(self, stream):
        self.stream = stream
        self.count = 0

    def read(self, size=-1):
        block = self.stream.read(size)
        self.count += len(block)
        return block


def inspect(entry, stream, seekable, want_hash, want_sections):
    """Fill the hash and the ELF section names of one regular file."""
    if want_sections:
        if not seekable:
            stream = io.BytesIO(stream.read())
        entry.sections = elf_section_names(stream)
        stream.seek(0)
    if want_hash:
        entry.sha256 = digest(stream)


def read_layer(layer, raw, hash_paths, elf_paths):
    """Read the members of one layer from `raw`, the seekable stream of the stored layer blob."""
    head = raw.read(2)
    raw.seek(0)
    counted = None
    try:
        if head == GZIP_MAGIC:
            layer.compressed = True
            counted = CountingReader(gzip.GzipFile(fileobj=raw))
            contents = tarfile.open(fileobj=counted, mode="r|")
        else:
            contents = tarfile.open(fileobj=raw, mode="r:")
    except tarfile.ReadError as error:
        if str(error) != "empty file":
            raise
        contents = None
    if contents is not None:
        with contents:
            read_members(layer, contents, counted is None, hash_paths, elf_paths)
    if counted is not None:
        while counted.read(CHUNK):
            pass
        layer.size = counted.count


def read_members(layer, contents, seekable, hash_paths, elf_paths):
    for member in contents:
        entry = Entry(member)
        parent, _, leaf = entry.path.rpartition("/")
        if leaf.startswith(".wh."):
            prefix = parent + "/" if parent else ""
            if leaf == ".wh..wh..opq":
                layer.opaque.append(prefix)
            else:
                layer.deleted.append(prefix + leaf[len(".wh."):])
            continue
        if entry.kind == "file":
            want_hash = hash_paths is not None and hash_paths(entry.path)
            want_sections = elf_paths is not None and elf_paths(entry.path)
            if want_hash or want_sections:
                inspect(entry, contents.extractfile(member), seekable, want_hash, want_sections)
        layer.entries[entry.path] = entry


def read_image(path, hash_paths=None, elf_paths=None):
    """Read an image archive. The predicates select the regular files that get a hash or ELF section names."""
    with tarfile.open(path) as archive:
        manifest = json.load(archive.extractfile("manifest.json"))
        if len(manifest) != 1:
            raise ValueError("The image archive must contain one image.")
        config = json.load(archive.extractfile(manifest[0]["Config"]))
        members = manifest[0]["Layers"]
        diff_ids = config["rootfs"]["diff_ids"]
        if len(members) != len(diff_ids):
            raise ValueError("The image configuration omits a layer.")
        layers = []
        for index, member in enumerate(members):
            info = archive.getmember(member)
            layer = Layer(index, diff_ids[index], member, info.size)
            read_layer(layer, archive.extractfile(info), hash_paths, elf_paths)
            layers.append(layer)
    return Image(path, manifest[0], config, layers)


def compressed_size(path, layer, level=6):
    """Return the gzip size of one layer. A stored gzip blob keeps its stored size."""
    if layer.compressed:
        return layer.stored_size
    total = 0
    compressor = zlib.compressobj(level, zlib.DEFLATED, 31)
    with tarfile.open(path) as archive:
        stream = archive.extractfile(layer.member)
        while True:
            block = stream.read(CHUNK)
            if not block:
                break
            total += len(compressor.compress(block))
    return total + len(compressor.flush())


def compressed_sizes(images, workers=4):
    """Return a map from diff ID to gzip size for every layer of the given images."""
    tasks = {}
    for image in images:
        for layer in image.layers:
            tasks.setdefault(layer.diff_id, (image.path, layer))
    with ThreadPoolExecutor(max_workers=max(1, workers)) as pool:
        futures = {diff_id: pool.submit(compressed_size, path, layer) for diff_id, (path, layer) in tasks.items()}
        return {diff_id: future.result() for diff_id, future in futures.items()}
