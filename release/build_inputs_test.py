import re
import shlex
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent


class ImageBuildInputsTests(unittest.TestCase):
    def test_release_context_contains_each_dockerfile_input(self):
        build = (ROOT / "release/build.sh").read_text()
        declaration = re.search(r"^for name in (.*?); do$", build, re.MULTILINE | re.DOTALL)
        self.assertIsNotNone(declaration, "The release build must declare its image inputs.")
        copied = set(shlex.split(declaration.group(1).replace("\\\n", " ")))
        generated = {"runner/image/mc-dispatch", "runner/image/${MC_BIN}", "runner/runtime-source/"}
        for name in ("Dockerfile", "runtime.Dockerfile"):
            recipe = (ROOT / "runner/image" / name).read_text().replace("\\\n", " ")
            required = set(re.findall(r"--mount=type=bind,source=([^,\s]+)", recipe))
            for line in recipe.splitlines():
                if not line.startswith("COPY "):
                    continue
                fields = shlex.split(line)
                if any(field.startswith("--from=") for field in fields):
                    continue
                required.update(field for field in fields[1:-1] if not field.startswith("--"))
            for source in sorted(required - generated):
                with self.subTest(dockerfile=name, source=source):
                    self.assertIn(source, copied, f"The release image context omits {source}.")
                    self.assertTrue((ROOT / source).is_file(), f"The source file is absent: {source}.")


if __name__ == "__main__":
    unittest.main()
