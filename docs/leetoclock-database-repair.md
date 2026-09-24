# One-time Leet o'Clock database repair

## Why this may be needed

The [original plugin added `GuildID` to `Game` in 2023](https://github.com/toksikk/gbp-leetoclock/commit/1078af08306b5e14375bc583a0632c2a7c96d6dd). Existing SQLite `games` tables could therefore have the physical column order `channel_id, game_date, season_id, guild_id` (SQLite adds new columns at the end). The prefixed `leetoclock_games` table in `gidbig.db` instead has `channel_id, guild_id, game_date, season_id`.

The [migration instructions for issue #178](https://github.com/toksikk/gidbig/issues/178#issuecomment-4414476684) copied the old table with `INSERT OR IGNORE INTO leetoclock_games SELECT * FROM old.games`, without naming columns. SQLite mapped values by position, shifting three fields in affected rows: `guild_id` received the game date, `game_date` received the season ID, and `season_id` received the guild ID. The affected rows were **recorded correctly before the copy**; the column-order mismatch occurred during the database consolidation, not during gameplay or because of an August change to game recording.

Deployments that started with a fresh `gidbig.db`, already repaired their old data, or copied it with explicitly matched columns do not need this repair. If `/leetoclock top` or `/leetoclock player` omits historical games from server/period queries, run the script below. It detects the shifted rows from their values, without assuming any date or row count; score rows are not modified.

## Repair

1. Stop the bot so the database cannot change during backup and repair. Locate its active SQLite file: `database.path` in `config.yaml`, otherwise `gidbig.db` in the bot's working directory.
2. From the repository root, run `python3 scripts/repair-leetoclock-games.py /path/to/gidbig.db`. Python 3.7 or newer and its standard library suffice. The script first makes a timestamped SQLite backup alongside the database, then repairs matching games in one transaction. It refuses unknown integer-date rows and rolls back if foreign-key validation fails.
3. Start the bot and check `/leetoclock top period:all` and `/leetoclock player period:all` in each server. Negative early-bird scores remain excluded from records by design.

Rerunning the command is safe: it reports zero changes and creates no new backup once all shifted games are repaired. Normal games are never touched. Keep the timestamped backup until you have checked the commands. To restore it while the bot is stopped, copy the backup over the database file. Keep local database copies and backups out of version control.
