import gzip
import hashlib
import io
import json
import struct
import tarfile
import tempfile
import unittest
from pathlib import Path

from image_archive import compressed_size, compressed_sizes, elf_section_names, read_image


def make_elf(names, extended=False):
    """Return a minimal ELF64 file whose section header table holds the given section names."""
    sections = [""] + list(names) + [".shstrtab"]
    strings = b"\0"
    offsets = {}
    for name in sections[1:]:
        offsets[name] = len(strings)
        strings += name.encode() + b"\0"
    strings_offset = 64
    table_offset = strings_offset + len(strings)
    table_offset += (-table_offset) % 8
    count = len(sections)
    names_index = count - 1
    header = bytearray(64)
    header[:4] = b"\x7fELF"
    header[4] = 2
    header[5] = 1
    header[6] = 1
    struct.pack_into("<HHIQQQIHHHHHH", header, 16, 2, 183, 1, 0, 0, table_offset, 0, 64, 56, 0, 64,
                     0 if extended else count, 0xFFFF if extended else names_index)
    table = b""
    for index, name in enumerate(sections):
        if index == 0:
            table += struct.pack("<IIQQQQIIQQ", 0, 0, 0, 0, 0, count if extended else 0, names_index if extended else 0, 0, 0, 0)
        elif name == ".shstrtab":
            table += struct.pack("<IIQQQQIIQQ", offsets[name], 3, 0, 0, strings_offset, len(strings), 0, 0, 1, 0)
        else:
            table += struct.pack("<IIQQQQIIQQ", offsets[name], 1, 0, 0, 0, 0, 0, 0, 1, 0)
    body = bytes(header) + strings
    body += b"\0" * (table_offset - len(body))
    return body + table


def member(kind, name, body=b"", mode=None, uid=0, gid=0, linkname=""):
    """Return one layer member as a tuple that `layer_tar` understands."""
    return (kind, name, body, mode, uid, gid, linkname)


def layer_tar(members):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w", format=tarfile.PAX_FORMAT) as layer:
        for kind, name, body, mode, uid, gid, linkname in members:
            item = tarfile.TarInfo(name)
            item.uid = uid
            item.gid = gid
            if kind == "file":
                item.size = len(body)
                item.mode = 0o644 if mode is None else mode
                layer.addfile(item, io.BytesIO(body))
                continue
            if kind == "dir":
                item.type = tarfile.DIRTYPE
                item.mode = 0o755 if mode is None else mode
            elif kind == "symlink":
                item.type = tarfile.SYMTYPE
                item.linkname = linkname
                item.mode = 0o777
            elif kind == "link":
                item.type = tarfile.LNKTYPE
                item.linkname = linkname
                item.mode = 0o644 if mode is None else mode
            else:
                raise ValueError(kind)
            layer.addfile(item)
    return buffer.getvalue()


def build_archive(directory, name, layers, history=None, legacy=False, compress=False):
    """Write an image archive with one layer tar per member list and return its path."""
    blobs = [layer_tar(members) for members in layers]
    diff_ids = ["sha256:" + hashlib.sha256(blob).hexdigest() for blob in blobs]
    if history is None:
        history = [{"created_by": "RUN step {}".format(index)} for index in range(len(blobs))]
    config = json.dumps({
        "os": "linux", "architecture": "arm64", "config": {},
        "rootfs": {"type": "layers", "diff_ids": diff_ids}, "history": history,
    }).encode()
    stored = [gzip.compress(blob, 6) if compress else blob for blob in blobs]
    if legacy:
        config_name = hashlib.sha256(config).hexdigest() + ".json"
        layer_names = ["{}/layer.tar".format(diff_id[7:]) for diff_id in diff_ids]
    else:
        config_name = "blobs/sha256/" + hashlib.sha256(config).hexdigest()
        layer_names = ["blobs/sha256/" + hashlib.sha256(blob).hexdigest() for blob in stored]
    path = Path(directory) / name
    with tarfile.open(path, "w") as archive:
        def add(member_name, body):
            item = tarfile.TarInfo(member_name)
            item.size = len(body)
            archive.addfile(item, io.BytesIO(body))
        add("manifest.json", json.dumps([{"Config": config_name, "RepoTags": [name], "Layers": layer_names}]).encode())
        add(config_name, config)
        for layer_name, blob in zip(layer_names, stored):
            add(layer_name, blob)
        if not legacy:
            add("oci-layout", b'{"imageLayoutVersion": "1.0.0"}')
            add("index.json", b'{"schemaVersion": 2, "manifests": []}')
    return str(path)


class ElfTest(unittest.TestCase):
    def test_section_names(self):
        self.assertEqual(elf_section_names(io.BytesIO(make_elf([".text", ".symtab", ".debug_info"]))),
                         [".text", ".symtab", ".debug_info", ".shstrtab"])

    def test_extended_section_numbering(self):
        self.assertEqual(elf_section_names(io.BytesIO(make_elf([".text", ".debug_line"], extended=True))),
                         [".text", ".debug_line", ".shstrtab"])

    def test_other_files(self):
        self.assertIsNone(elf_section_names(io.BytesIO(b"#!/bin/sh\necho hello\n")))
        self.assertIsNone(elf_section_names(io.BytesIO(b"\x7fELF")))

    def test_file_without_section_headers(self):
        body = bytearray(make_elf([".text"]))
        struct.pack_into("<Q", body, 0x28, 0)
        self.assertEqual(elf_section_names(io.BytesIO(bytes(body))), [])


class ReadImageTest(unittest.TestCase):
    def layers(self):
        return [
            [member("dir", "opt"), member("file", "opt/tool", b"tool one", mode=0o755),
             member("file", "usr/bin/su", b"su", mode=0o4755, uid=0, gid=0),
             member("file", "etc/old", b"old"), member("dir", "var/cache"), member("file", "var/cache/a", b"a")],
            [member("file", "opt/program", make_elf([".text", ".symtab"]), mode=0o755, uid=7, gid=8),
             member("link", "opt/program-link", linkname="opt/program"),
             member("symlink", "usr/local/bin/tool", linkname="/opt/tool"),
             member("file", "etc/.wh.old"), member("file", "var/cache/.wh..wh..opq"), member("file", "var/cache/b", b"b")],
        ]

    def check_image(self, image):
        self.assertEqual(len(image.layers), 2)
        first, second = image.layers
        self.assertEqual(first.entries["opt/tool"].kind, "file")
        self.assertEqual(first.entries["opt/tool"].sha256, hashlib.sha256(b"tool one").hexdigest())
        self.assertIsNone(first.entries["opt/tool"].sections)
        self.assertEqual(first.entries["usr/bin/su"].mode, 0o4755)
        program = second.entries["opt/program"]
        self.assertEqual(program.sections, [".text", ".symtab", ".shstrtab"])
        self.assertEqual((program.uid, program.gid, program.mode), (7, 8, 0o755))
        self.assertEqual(second.entries["opt/program-link"].kind, "link")
        self.assertIs(second.resolve(second.entries["opt/program-link"]), program)
        self.assertEqual(second.entries["usr/local/bin/tool"].linkname, "opt/tool")
        self.assertEqual(second.deleted, ["etc/old"])
        self.assertEqual(second.opaque, ["var/cache/"])
        view = image.merged()
        self.assertNotIn("etc/old", view)
        self.assertNotIn("var/cache/a", view)
        self.assertIn("var/cache/b", view)
        self.assertIn("var/cache", view)
        self.assertEqual(view["opt/program"][0], 1)
        self.assertEqual(image.history(), ["RUN step 0", "RUN step 1"])

    def test_oci_layout(self):
        with tempfile.TemporaryDirectory() as directory:
            path = build_archive(directory, "image.tar", self.layers())
            image = read_image(path, hash_paths=lambda p: p.startswith("opt/"), elf_paths=lambda p: p.startswith("opt/"))
            self.check_image(image)
            self.assertFalse(image.layers[0].compressed)
            self.assertEqual(image.layers[0].size, image.layers[0].stored_size)

    def test_legacy_layout(self):
        with tempfile.TemporaryDirectory() as directory:
            path = build_archive(directory, "image.tar", self.layers(), legacy=True)
            self.check_image(read_image(path, hash_paths=lambda p: p.startswith("opt/"), elf_paths=lambda p: p.startswith("opt/")))

    def test_compressed_layers(self):
        with tempfile.TemporaryDirectory() as directory:
            path = build_archive(directory, "image.tar", self.layers(), compress=True)
            image = read_image(path, hash_paths=lambda p: p.startswith("opt/"), elf_paths=lambda p: p.startswith("opt/"))
            self.check_image(image)
            layer = image.layers[1]
            self.assertTrue(layer.compressed)
            self.assertEqual(layer.size, len(layer_tar(self.layers()[1])))
            self.assertLess(layer.stored_size, layer.size)
            self.assertEqual(compressed_size(path, layer), layer.stored_size)

    def test_predicates_select_the_inspected_files(self):
        with tempfile.TemporaryDirectory() as directory:
            image = read_image(build_archive(directory, "image.tar", self.layers()))
            self.assertIsNone(image.layers[0].entries["opt/tool"].sha256)
            self.assertIsNone(image.layers[1].entries["opt/program"].sections)

    def test_compressed_sizes(self):
        with tempfile.TemporaryDirectory() as directory:
            layers = self.layers()
            path = build_archive(directory, "image.tar", layers)
            image = read_image(path)
            sizes = compressed_sizes((image, image), workers=2)
            self.assertEqual(set(sizes), set(image.diff_ids))
            for layer, members in zip(image.layers, layers):
                self.assertEqual(sizes[layer.diff_id], len(gzip.compress(layer_tar(members), 6)))

    def test_empty_layers(self):
        with tempfile.TemporaryDirectory() as directory:
            for compress in (False, True):
                path = build_archive(directory, "image.tar", [[], [member("file", "etc/a", b"a")], []], compress=compress)
                image = read_image(path)
                self.assertEqual([len(layer.entries) for layer in image.layers], [0, 1, 0])
                self.assertEqual(image.layers[0].compressed, compress)
                self.assertEqual(image.layers[2].size, 10240)
                self.assertEqual(image.layers[2].stored_size < 10240, compress)
                self.assertEqual(set(image.merged()), {"etc/a"})

    def test_rejects_an_archive_with_two_images(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "two.tar"
            with tarfile.open(path, "w") as archive:
                body = json.dumps([{"Config": "a", "Layers": []}, {"Config": "b", "Layers": []}]).encode()
                item = tarfile.TarInfo("manifest.json")
                item.size = len(body)
                archive.addfile(item, io.BytesIO(body))
            with self.assertRaises(ValueError):
                read_image(str(path))


if __name__ == "__main__":
    unittest.main()
