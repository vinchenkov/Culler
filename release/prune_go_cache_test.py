import unittest

from prune_go_cache import obsolete_caches, prune


def cache(number, job="fast-suite-macos", ref="refs/heads/main", platform="macOS-ARM64"):
    return {"id": number, "key": "culler-go-build-v1-" + platform + "-" + job + "-" + "a" * 64 + "-" + format(number, "040x"),
            "ref": ref, "created_at": "2026-11-06T12:00:00Z"}


class CacheTest(unittest.TestCase):
    def test_cleanup_preserves_images_dependencies_other_refs_and_two_caches_per_group(self):
        rows = [cache(n) for n in range(1, 5)]
        rows += [cache(n, job="application-package") for n in range(5, 8)]
        rows += [cache(8, platform="Linux-ARM64"), cache(9, ref="refs/pull/1/merge")]
        rows += [{**cache(10), "key": "release-images-Linux-ARM64-" + "a" * 40},
                 {**cache(11), "key": "culler-dependencies-v1-macOS-ARM64"},
                 {**cache(12), "key": "culler-go-build-v1-unknown"}]
        self.assertEqual(set(obsolete_caches(rows)), {1, 2, 5})

    def test_cleanup_collects_every_page_before_deletion(self):
        calls = []

        class API:
            def request(self, method, path, **kwargs):
                calls.append((method, path))
                if method == "GET":
                    return {"actions_caches": [cache(n) for n in (range(1, 101) if path.endswith("page=1") else [101])]}
                if not kwargs["binary"]:
                    raise AssertionError("The delete response has no JSON body.")

        self.assertEqual(prune(API()), 99)
        self.assertEqual([method for method, _ in calls[:2]], ["GET", "GET"])
        deleted = {int(path.rsplit("/", 1)[1]) for method, path in calls if method == "DELETE"}
        self.assertEqual(deleted, set(range(1, 100)))
