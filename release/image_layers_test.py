import io
import json
import tarfile
import tempfile
import unittest
from pathlib import Path

from image_layers import verify


def add(archive, name, body):
    item = tarfile.TarInfo(name)
    item.size = len(body)
    archive.addfile(item, io.BytesIO(body))


class ImageLayersTest(unittest.TestCase):
    def archive(self, directory, layers, env=()):
        path = Path(directory) / "image.tar"
        with tarfile.open(path, "w") as image:
            add(image, "manifest.json", json.dumps([{
                "Config": "config.json", "Layers": [str(i) + "/layer.tar" for i in range(len(layers))],
            }]).encode())
            add(image, "config.json", json.dumps({
                "os": "linux", "architecture": "arm64", "config": {"Env": list(env)},
            }).encode())
            for i, names in enumerate(layers):
                buffer = io.BytesIO()
                with tarfile.open(fileobj=buffer, mode="w") as layer:
                    for name in names:
                        add(layer, name, b"file")
                add(image, str(i) + "/layer.tar", buffer.getvalue())
        return path

    def test_product_content(self):
        with tempfile.TemporaryDirectory() as directory:
            verify(self.archive(directory, [["opt/bun/bin/bun", "opt/go/bin/go", "usr/local/bin/mise", "home/mc-model/.agents/skills"]]))

    def test_deleted_path_remains_in_an_earlier_layer(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.archive(directory, [["tmp/go-mod-cache/cache/download"], ["tmp/.wh.go-mod-cache"]])
            with self.assertRaises(ValueError):
                verify(path)

    def test_unnamed_build_settings(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.archive(directory, [[]], ["GOCACHE=/tmp/build"])
            with self.assertRaises(ValueError):
                verify(path)

    def test_build_tag_settings(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.archive(directory, [[]], ["GOFLAGS=-tags=test_scratch_spine"])
            with self.assertRaises(ValueError):
                verify(path)

    def test_operator_state(self):
        with tempfile.TemporaryDirectory() as directory:
            for name in ("root/.culler/state", "home/mc-model/.mission-control/spine.db",
                         "workspace/OPERATOR-INPUTS.md", "tmp/go-build-cache/trim.txt"):
                path = self.archive(directory, [[name]])
                with self.assertRaises(ValueError):
                    verify(path)


if __name__ == "__main__":
    unittest.main()
