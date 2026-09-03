import json
import os
import shutil
import sqlite3
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[1]


class BackupScriptTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls._backend_dir = tempfile.TemporaryDirectory()
        cls.backend = None
        if shutil.which("go"):
            binary = Path(cls._backend_dir.name) / "feader-rss-fetch"
            build_env = {**os.environ, "GOCACHE": str(Path(cls._backend_dir.name) / "gocache")}
            built = subprocess.run(
                ["go", "build", "-o", str(binary), "./cmd/feader-rss-fetch"],
                cwd=ROOT,
                env=build_env,
                capture_output=True,
                text=True,
            )
            if built.returncode == 0:
                cls.backend = binary

    @classmethod
    def tearDownClass(cls):
        cls._backend_dir.cleanup()

    def _env(self, config_home, state_home):
        env = {**os.environ, "XDG_CONFIG_HOME": str(config_home), "XDG_STATE_HOME": str(state_home)}
        if self.backend is not None:
            env["FEADER_RSS_FETCH"] = str(self.backend)
        return env

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
            env = self._env(config_home, state_home)

            created = subprocess.run([str(ROOT / "scripts/backup.sh")], env=env, check=True, capture_output=True, text=True)
            self.assertIn("Backup created", created.stdout)
            backups = sorted((state_home / "omarchy/rss-reader/backups").iterdir())
            self.assertEqual(len(backups), 1)

            config_file.write_text("changed")
            state_file.write_text("changed")
            restored = subprocess.run([str(ROOT / "scripts/restore.sh"), str(backups[0])], env=env, check=True, capture_output=True, text=True)
            self.assertIn("Backup restored", restored.stdout)
            self.assertEqual(json.loads(config_file.read_text())["feeds"][0]["name"], "One")
            self.assertTrue(json.loads(state_file.read_text())["items"][0]["read"])

    def test_sqlite_backup_round_trip_and_restore_over_non_empty_database(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            config_home = base / "config"
            state_home = base / "state"
            config_file = config_home / "omarchy" / "rss-reader.json"
            state_dir = state_home / "omarchy" / "rss-reader"
            db_file = state_dir / "items.db"
            config_file.parent.mkdir(parents=True)
            state_dir.mkdir(parents=True)
            config_file.write_text(json.dumps({"feeds": [{"name": "One", "url": "https://one.test"}]}))
            (state_dir / "preferences.json").write_text(json.dumps({"preferences": {"readFilter": "unread"}}))

            source = sqlite3.connect(db_file)
            source.execute("PRAGMA journal_mode=WAL")
            source.executescript(
                """
                CREATE TABLE articles (
                    id TEXT PRIMARY KEY,
                    feed TEXT NOT NULL,
                    title TEXT NOT NULL,
                    url TEXT NOT NULL,
                    published TEXT NOT NULL DEFAULT '',
                    published_at INTEGER NOT NULL DEFAULT 0,
                    summary TEXT NOT NULL DEFAULT '',
                    author TEXT NOT NULL DEFAULT '',
                    categories TEXT NOT NULL DEFAULT '',
                    content TEXT,
                    read INTEGER NOT NULL DEFAULT 0,
                    first_seen TEXT NOT NULL
                );
                CREATE INDEX idx_articles_published_at ON articles(published_at DESC, first_seen DESC);
                """
            )
            source.execute(
                """
                INSERT INTO articles
                    (id, feed, title, url, published, published_at, summary, author, categories, content, read, first_seen)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                ("one", "Feed", "Original", "https://one.test/article", "2024-01-01", 1704067200,
                 "Summary", "Alice", '["News"]', "Cached content", 1, "2024-01-01T00:00:00Z"),
            )
            source.commit()
            env = self._env(config_home, state_home)

            created = subprocess.run([str(ROOT / "scripts/backup.sh")], env=env, check=True, capture_output=True, text=True)
            self.assertIn("Backup created", created.stdout)
            backups = sorted((state_dir / "backups").iterdir())
            self.assertEqual(len(backups), 1)
            backup_dir = backups[0]
            backup_db = backup_dir / "items.db"
            self.assertTrue(backup_db.is_file())
            self.assertFalse(Path(str(backup_db) + "-wal").exists())
            self.assertFalse(Path(str(backup_db) + "-shm").exists())
            with sqlite3.connect(backup_db) as snapshot:
                row = snapshot.execute("SELECT id, title, content, read FROM articles").fetchone()
            self.assertEqual(row, ("one", "Original", "Cached content", 1))
            source.close()

            with sqlite3.connect(db_file) as target:
                target.execute("UPDATE articles SET title = 'Changed', content = NULL, read = 0 WHERE id = 'one'")
                target.execute(
                    "INSERT INTO articles (id, feed, title, url, first_seen) VALUES (?, ?, ?, ?, ?)",
                    ("extra", "Feed", "Extra", "https://one.test/extra", "2024-01-02T00:00:00Z"),
                )
            config_file.write_text("changed")
            (state_dir / "preferences.json").write_text("changed")

            restored = subprocess.run(
                [str(ROOT / "scripts/restore.sh"), str(backup_dir)],
                env=env,
                check=True,
                capture_output=True,
                text=True,
            )
            self.assertIn("Backup restored", restored.stdout)
            self.assertEqual(json.loads(config_file.read_text())["feeds"][0]["name"], "One")
            self.assertEqual(json.loads((state_dir / "preferences.json").read_text())["preferences"]["readFilter"], "unread")
            with sqlite3.connect(db_file) as restored_db:
                rows = restored_db.execute("SELECT id, title, content, read FROM articles").fetchall()
            self.assertEqual(rows, [("one", "Original", "Cached content", 1)])


if __name__ == "__main__":
    unittest.main()
