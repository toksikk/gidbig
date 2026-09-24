"""Run with: python3 -m unittest discover -s scripts -p 'test_*.py'."""

import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("repair-leetoclock-games.py")


class RepairLeetoclockGamesTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.database = Path(self.temp.name) / "gidbig.db"
        with sqlite3.connect(self.database) as db:
            db.executescript("""
                PRAGMA foreign_keys = OFF;
                CREATE TABLE leetoclock_seasons (id INTEGER PRIMARY KEY);
                CREATE TABLE leetoclock_games (
                    id INTEGER PRIMARY KEY, channel_id TEXT NOT NULL,
                    guild_id TEXT NOT NULL, game_date DATETIME NOT NULL,
                    season_id INTEGER NOT NULL REFERENCES leetoclock_seasons(id)
                );
                CREATE TABLE leetoclock_scores (id INTEGER PRIMARY KEY, game_id INTEGER, score INTEGER);
                INSERT INTO leetoclock_seasons VALUES (7);
                INSERT INTO leetoclock_games VALUES
                    (1, 'channel', '2024-02-19 13:37:00+01:00', 7, 225303764108705793),
                    (2, 'another', '125231125961506816', '2026-09-24 13:37:00+02:00', 7);
                INSERT INTO leetoclock_scores VALUES (1, 1, 0), (2, 2, 17);
            """)

    def run_repair(self):
        return subprocess.run(
            [sys.executable, SCRIPT, self.database], capture_output=True, text=True, check=False
        )

    def test_repairs_once_without_touching_scores_or_new_games(self):
        first = self.run_repair()
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertIn("Repaired 1 games", first.stdout)
        backups = list(self.database.parent.glob("gidbig.db.backup-*"))
        self.assertEqual(len(backups), 1)
        with sqlite3.connect(self.database) as db:
            self.assertEqual(db.execute("SELECT * FROM leetoclock_games WHERE id=1").fetchone(),
                             (1, "channel", "225303764108705793", "2024-02-19 13:37:00+01:00", 7))
            self.assertEqual(db.execute("SELECT * FROM leetoclock_games WHERE id=2").fetchone(),
                             (2, "another", "125231125961506816", "2026-09-24 13:37:00+02:00", 7))
            self.assertEqual(db.execute("SELECT * FROM leetoclock_scores").fetchall(),
                             [(1, 1, 0), (2, 2, 17)])
            self.assertEqual(db.execute("PRAGMA foreign_key_check").fetchall(), [])
        with sqlite3.connect(backups[0]) as saved:
            self.assertEqual(saved.execute("SELECT typeof(game_date) FROM leetoclock_games WHERE id=1").fetchone(),
                             ("integer",))
        second = self.run_repair()
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertIn("database unchanged", second.stdout)
        self.assertEqual(len(list(self.database.parent.glob("gidbig.db.backup-*"))), 1)

    def test_refuses_unrecognized_rows_without_modifying_database(self):
        with sqlite3.connect(self.database) as db:
            db.execute("UPDATE leetoclock_games SET season_id=0 WHERE id=1")
        result = self.run_repair()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unrecognized historical game", result.stderr)
        self.assertEqual(list(self.database.parent.glob("gidbig.db.backup-*")), [])
        with sqlite3.connect(self.database) as db:
            self.assertEqual(db.execute("SELECT season_id FROM leetoclock_games WHERE id=1").fetchone(), (0,))


if __name__ == "__main__":
    unittest.main()
