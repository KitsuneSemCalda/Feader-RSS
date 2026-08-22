import json
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[1]


class ManifestTests(unittest.TestCase):
    def test_manifest_points_to_existing_safe_entry_point(self):
        manifest = json.loads((ROOT / "manifest.json").read_text())

        self.assertEqual(manifest["schemaVersion"], 1)
        self.assertEqual(manifest["id"], "io.github.kitsunesemcalda.feader-rss")
        self.assertEqual(manifest["kinds"], ["bar-widget"])

        entry_point = manifest["entryPoints"]["barWidget"]
        self.assertFalse(entry_point.startswith("/"))
        self.assertNotIn("..", Path(entry_point).parts)
        self.assertTrue((ROOT / entry_point).is_file())


if __name__ == "__main__":
    unittest.main()
