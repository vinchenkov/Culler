"""Present every retained file to the secret scanner without path exclusions."""

import bz2
import codecs
import gzip
import hashlib
import io
import lzma
import shutil
import tarfile
import zipfile
from pathlib import Path


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


class Content:
    def __init__(self, root):
        self.root = root
        self.root.mkdir()
        self.locations = {}
        self.bytes = 0

    def add(self, stream, location, depth=0):
        if depth > 16:
            raise ValueError("The nested archive exceeds the required scan depth.")
        number = len(self.locations)
        name = f"{number:08}.txt"
        raw = self.root.parent / "content.bin"
        with raw.open("wb") as output:
            shutil.copyfileobj(stream, output)
        self.bytes += raw.stat().st_size
        if self.bytes > 80 * 1024**3:
            raise ValueError("The extracted contents exceed the scan limit.")
        self.locations[name] = location
        # Binary files retain their text and line positions. NUL bytes cannot cause a skipped file.
        decoder = codecs.getincrementaldecoder("utf-8")("replace")
        with raw.open("rb") as source, (self.root / name).open("w", encoding="utf-8") as output:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                output.write(decoder.decode(block).replace("\0", " "))
            output.write(decoder.decode(b"", final=True).replace("\0", " "))
        saved = self.root.parent / f"archive-{depth}.bin"
        raw.rename(saved)
        try:
            with saved.open("rb") as source:
                magic = source.read(8)
            if zipfile.is_zipfile(saved):
                with zipfile.ZipFile(saved) as archive:
                    for item in archive.infolist():
                        if not item.is_dir():
                            with archive.open(item) as source:
                                self.add(source, {**location, "file": location["file"] + "!" + item.filename}, depth + 1)
            elif tarfile.is_tarfile(saved):
                with tarfile.open(saved) as archive:
                    self.tar(archive, location, depth + 1)
            elif magic.startswith((b"\x1f\x8b", b"BZh", b"\xfd7zXZ\0")):
                opener = gzip.open if magic.startswith(b"\x1f\x8b") else bz2.open if magic.startswith(b"BZh") else lzma.open
                with opener(saved, "rb") as source:
                    self.add(source, {**location, "file": location["file"] + "!contents"}, depth + 1)
            elif location["file"].lower().endswith((".zip", ".jar", ".tar", ".tgz", ".tar.gz", ".gz", ".xz", ".bz2")):
                raise ValueError("A required archive cannot be read.")
        finally:
            saved.unlink(missing_ok=True)

    def tar(self, archive, location, depth=0):
        for item in archive:
            target = {**location, "file": location["file"] + "!" + item.name}
            if item.isfile():
                with archive.extractfile(item) as source:
                    self.add(source, target, depth)
            elif item.issym() or item.islnk():
                self.add(io.BytesIO(item.linkname.encode()), target, depth)
            elif not (item.isdir() or item.isdev() or item.isfifo()):
                raise ValueError("An archive contains an unsupported file type.")
