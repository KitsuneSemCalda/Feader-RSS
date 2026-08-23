import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[1]


class BackupScriptTests(unittest.TestCase):
    def test_backup_and_restore_round_trip(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            config_home = base / "config"
            state_home = base / "state"
            config_file = config_home / "omarchy" / "rss-reader.json"
            state_file = state_home / "omarchy" / "rss-reader" / "items.json"
            config_file.parent.mkdir(parents=True)
            state_file.parent.mkdir(parents=True)
            config_file.write_text(json.dumps({"feeds": [{"name": "One", "url": "https://one.test"}]}))
            state_file.write_text(json.dumps({"items": [{"id": "one", "read": True}]}))
            env = {**os.environ, "XDG_CONFIG_HOME": str(config_home), "XDG_STATE_HOME": str(state_home)}

            created = subprocess.run([str(ROOT / "scripts/backup.sh")], env=env, check=True, capture_output=True, text=True)
            self.assertIn("Backup criado", created.stdout)
            backups = sorted((state_home / "omarchy/rss-reader/backups").iterdir())
            self.assertEqual(len(backups), 1)

            config_file.write_text("changed")
            state_file.write_text("changed")
            restored = subprocess.run([str(ROOT / "scripts/restore.sh"), str(backups[0])], env=env, check=True, capture_output=True, text=True)
            self.assertIn("Backup restaurado", restored.stdout)
            self.assertEqual(json.loads(config_file.read_text())["feeds"][0]["name"], "One")
            self.assertTrue(json.loads(state_file.read_text())["items"][0]["read"])


if __name__ == "__main__":
    unittest.main()
