#!/usr/bin/env python3
"""Repair shifted columns in historical leetoclock_games rows (one time)."""

import argparse
import sqlite3
from datetime import datetime
from pathlib import Path


LEGACY = "typeof(game_date) = 'integer'"
INVALID = """(
    season_id <= 10000000000000000
    OR julianday(guild_id) IS NULL
    OR NOT EXISTS (SELECT 1 FROM leetoclock_seasons WHERE id = leetoclock_games.game_date)
)"""


def repair(path: Path):
    if not path.is_file():
        raise ValueError(f"database does not exist: {path}")

    # mode=rw prevents sqlite3 from silently creating a new database.
    with sqlite3.connect(path.resolve().as_uri() + "?mode=rw", uri=True) as db:
        db.execute("PRAGMA foreign_keys = ON")
        count = db.execute(
            f"SELECT COUNT(*) FROM leetoclock_games WHERE {LEGACY}"
        ).fetchone()[0]
        if count == 0:
            return 0, None

        if db.execute(
            f"SELECT id FROM leetoclock_games WHERE {LEGACY} AND {INVALID} LIMIT 1"
        ).fetchone():
            raise ValueError("unrecognized historical game row; database left unchanged")

        backup = path.with_name(path.name + ".backup-" + datetime.now().strftime("%Y%m%d-%H%M%S"))
        if backup.exists():
            raise FileExistsError(f"backup already exists: {backup}")
        with sqlite3.connect(backup) as saved:
            db.backup(saved)

        # SQLite evaluates assignments against original row values. The old
        # rows hold date in guild_id, season number in game_date, and guild ID
        # in season_id. New rows have a TEXT game_date and are never updated.
        try:
            db.execute("BEGIN IMMEDIATE")
            if db.execute(
                f"SELECT id FROM leetoclock_games WHERE {LEGACY} AND {INVALID} LIMIT 1"
            ).fetchone():
                raise ValueError("historical games changed since backup; database left unchanged")
            result = db.execute(
                f"""UPDATE leetoclock_games
                    SET guild_id = CAST(season_id AS TEXT),
                        game_date = guild_id,
                        season_id = game_date
                    WHERE {LEGACY}"""
            )
            if result.rowcount != count or db.execute(
                f"SELECT 1 FROM leetoclock_games WHERE {LEGACY} LIMIT 1"
            ).fetchone():
                raise ValueError("unexpected game count; transaction rolled back")
            if db.execute("PRAGMA foreign_key_check").fetchone():
                raise ValueError("foreign key check failed; transaction rolled back")
            db.commit()
        except Exception:
            db.rollback()
            raise
        return count, backup


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("database", type=Path, help="path to gidbig SQLite database")
    args = parser.parse_args()
    try:
        updated, backup_path = repair(args.database)
    except (ValueError, FileExistsError, sqlite3.Error) as exc:
        parser.exit(1, f"Repair failed: {exc}\n")
    if backup_path:
        print(f"Repaired {updated} games. Backup: {backup_path}")
    else:
        print("No historical games need repair; database unchanged.")
