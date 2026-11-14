import hashlib
import io
import json
import tarfile
import tempfile
import unittest
from pathlib import Path

from image_load import archive_identity

COMMIT = "a" * 40


def add(archive, name, body):
    item = tarfile.TarInfo(name)
    item.size = len(body)
    archive.addfile(item, io.BytesIO(body))


class ImageLoadTest(unittest.TestCase):
    def archive(self, directory, revision):
        config = json.dumps({"os": "linux", "architecture": "arm64",
                             "config": {"Labels": {"org.opencontainers.image.revision": revision}}}).encode()
        path = Path(directory) / "image.tar"
        with tarfile.open(path, "w") as image:
            add(image, "manifest.json", json.dumps([{"Config": "config.json", "Layers": []}]).encode())
            add(image, "config.json", config)
        return path, "sha256:" + hashlib.sha256(config).hexdigest()

    def test_the_identity_is_the_digest_of_the_image_configuration(self):
        with tempfile.TemporaryDirectory() as directory:
            path, identity = self.archive(directory, COMMIT)
            self.assertEqual(archive_identity(path, COMMIT), identity)

    def test_an_image_of_another_commit_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            path, _ = self.archive(directory, "b" * 40)
            with self.assertRaises(ValueError):
                archive_identity(path, COMMIT)


if __name__ == "__main__":
    unittest.main()
