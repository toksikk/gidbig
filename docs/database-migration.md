# Consolidated database migration

## Background

Early versions of gidbig stored data in one SQLite file per module:

| Module | Legacy file |
|---|---|
| coffee | `coffee.db` |
| leetoclock | `plugins/leetoclock.sqlite` |
| gippity | `gippity.db` |

Current versions use a single shared database, `gidbig.db`, in the bot's working directory, with a per-module table-name prefix (`coffee_*`, `leetoclock_*`, `gippity_*`). The prefixes let the modules share one file without table-name collisions, and give you one file to back up.

This guide moves the data from a legacy file into the shared database. It is written so it works for any module and any file layout; substitute your own paths.

## Who needs this

You need this migration only if you are upgrading from a release that still used a per-module SQLite file **and** you want to keep the existing data. A fresh install needs nothing: the bot creates the shared database on first start.

If your bot already starts without error and `/status` shows the expected data, you are already migrated. Check the shared database for the prefixed tables:

```bash
sqlite3 gidbig.db ".tables"
```

## Important caveats

- **The bot must be stopped** while you copy. If it starts before the copy, the new schema is created empty and the data appears lost. The data is still in the legacy file and you can recover it (see [Recovering](#recovering-from-a-started-bot)).
- **Run the copy exactly once.** `INSERT OR IGNORE` only skips rows that violate a constraint. Most history tables have no unique constraint, so a second run duplicates every row.
- **Never use `SELECT *`** for the copy. The new tables may add an `id` column or reorder columns, so positional mapping silently corrupts fields. Always name the columns.
- **Keep database files and backups out of version control.** The repository `.gitignore` already covers `*.db`, `*.db-wal`, `*.db-shm`, `*.db.bak`, and `*.db.pre-*.bak`.
- **SQLite WAL files matter.** If the source database is in WAL mode, a plain `cp` can miss recent data. Use `sqlite3 ... ".backup"` (shown below) or stop the bot first so the WAL is checkpointed.

## Step 1 — Locate the databases

The shared database is `gidbig.db` in the bot's working directory. Locate that file and the legacy file for the module you are migrating. Both are relative to the bot's working directory unless absolute.

Confirm the legacy file holds data and inspect its schema:

```bash
sqlite3 gippity.db ".tables"
sqlite3 gippity.db ".schema"
```

Note whether each table has an `id` primary key column. You will name columns explicitly later, but knowing the source schema avoids surprises.

## Step 2 — Stop the bot and back up

```bash
systemctl stop gidbig          # or however the bot is managed

# WAL-safe backups, next to the originals
sqlite3 gippity.db ".backup gippity.db.pre-migration.bak"
sqlite3 gidbig.db  ".backup gidbig.db.pre-migration.bak"
```

Keep both backups until you have checked the migrated data.

## Step 3 — Create the prefixed tables and copy

Run the copy from the bot's working directory. Adjust the file name, the module prefix, the table names, and the column lists to match what you saw in Step 1.

The example below migrates gippity. The same pattern applies to any module: target table = `<module>_<legacy table>`, and every column listed explicitly on both sides.

Create the target tables with the schema the bot's ORM expects. For gippity that schema is:

```sql
CREATE TABLE IF NOT EXISTS gippity_chat_history (
  id integer PRIMARY KEY AUTOINCREMENT,
  user_id text, channel_id text, timestamp integer, message text,
  message_id text, guild_id text, is_bot_mention numeric
);
CREATE INDEX IF NOT EXISTS idx_gippity_chat_history_user_id    ON gippity_chat_history(user_id);
CREATE INDEX IF NOT EXISTS idx_gippity_chat_history_channel_id ON gippity_chat_history(channel_id);
CREATE INDEX IF NOT EXISTS idx_gippity_chat_history_message_id ON gippity_chat_history(message_id);
CREATE INDEX IF NOT EXISTS idx_gippity_chat_history_guild_id   ON gippity_chat_history(guild_id);

CREATE TABLE IF NOT EXISTS gippity_user_privacy (
  user_id text, privacy_enabled numeric NOT NULL, PRIMARY KEY (user_id)
);

CREATE TABLE IF NOT EXISTS gippity_chat_attachments (
  id integer PRIMARY KEY AUTOINCREMENT,
  message_id text, attachment_url text, image_description text
);
CREATE INDEX IF NOT EXISTS idx_gippity_chat_attachments_message_id ON gippity_chat_attachments(message_id);

CREATE TABLE IF NOT EXISTS gippity_chat_history_edits (
  id integer PRIMARY KEY AUTOINCREMENT,
  original_message_id text, edited_content text, version integer, edited_at integer
);
CREATE INDEX IF NOT EXISTS idx_gippity_chat_history_edits_original
  ON gippity_chat_history_edits(original_message_id, version);
```

Then attach the legacy file and copy the rows:

```bash
sqlite3 gidbig.db <<'SQL'
ATTACH DATABASE 'gippity.db' AS old;

-- Copy rows with explicit column lists (never SELECT *).
INSERT OR IGNORE INTO gippity_chat_history
  (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention)
  SELECT user_id, channel_id, timestamp, message, message_id, guild_id,
         COALESCE(is_bot_mention, 0)
  FROM old.chat_history;

INSERT OR IGNORE INTO gippity_user_privacy (user_id, privacy_enabled)
  SELECT user_id, privacy_enabled FROM old.user_privacy;

INSERT OR IGNORE INTO gippity_chat_attachments
  (message_id, attachment_url, image_description)
  SELECT message_id, attachment_url, image_description FROM old.chat_attachments;

INSERT OR IGNORE INTO gippity_chat_history_edits
  (original_message_id, edited_content, version, edited_at)
  SELECT original_message_id, edited_content, version, edited_at
  FROM old.chat_history_edits;

DETACH DATABASE old;
SQL
```

Notes:

- `id` columns are primary keys and are intentionally omitted from the `INSERT` column lists so SQLite assigns them; the legacy values are not needed once the rows are self-consistent.
- `COALESCE(is_bot_mention, 0)` handles legacy rows written before that column existed.
- If the legacy table has an `id` column that other tables reference, include `id` in both column lists to preserve the linkage.
- Creating the tables by hand avoids a SQLite limitation: you cannot add a `PRIMARY KEY` column to an existing table with `ALTER TABLE`. If a prefixed table already exists without its `id` column, the bot's schema migration fails; drop the empty table and rerun the statements above.

## Step 4 — Verify

Row counts must match between source and target:

```bash
for t in chat_history user_privacy chat_attachments chat_history_edits; do
  old=$(sqlite3 gippity.db "SELECT COUNT(*) FROM $t;")
  new=$(sqlite3 gidbig.db "SELECT COUNT(*) FROM gippity_$t;")
  echo "$t: old=$old new=$new"
done
```

For a stronger check, compare a hash of the ordered rows. Identical hashes prove the content, not just the row count, was copied:

```bash
echo -n "old: "; sqlite3 gippity.db \
  "SELECT user_id,channel_id,timestamp,message,message_id,guild_id,COALESCE(is_bot_mention,0) FROM chat_history ORDER BY message_id;" \
  | shasum -a 256
echo -n "new: "; sqlite3 gidbig.db \
  "SELECT user_id,channel_id,timestamp,message,message_id,guild_id,COALESCE(is_bot_mention,0) FROM gippity_chat_history ORDER BY message_id;" \
  | shasum -a 256
```

Run `sqlite3 gidbig.db "PRAGMA integrity_check;"` as a final sanity check.

## Step 5 — Update configuration and deploy

1. Confirm the bot uses the shared `gidbig.db` in its working directory (no database path is configured any more; location is set by the working directory or a container volume mount).
2. Remove any legacy-file mounts from your deployment. For example, drop the `gippity.db` bind mount and any `data/gippity.db` volume, keeping only the shared database file.
3. Deploy the new binary and start the bot.
4. Watch the logs for database errors, and confirm the data through the bot's own commands.

## Recovering from a started bot

If the bot started before the copy, it created the prefixed tables empty in the shared database. The legacy file is untouched, so simply stop the bot and rerun Step 3. If the bot also wrote new rows in the meantime, those rows remain; the copy appends the legacy rows without removing them.

If you accidentally ran the copy twice, the tables without a unique constraint now contain duplicates. Restore the shared database from `gidbig.db.pre-migration.bak` and run the copy once.

## Step 6 — Clean up

After the bot has run correctly for a stable period (for example, a few weeks), delete the legacy file and its backups:

```bash
rm -f gippity.db gippity.db-wal gippity.db-shm
rm -f gippity.db.pre-migration.bak gidbig.db.pre-migration.bak
```

## Appendix — legacy layout reference

The exact legacy files depend on your install history. Use `.tables` and `.schema` on your own files; the table below lists the names used by the historical releases.

| Module | Legacy file | Legacy tables | Prefixed tables in `gidbig.db` |
|---|---|---|---|
| gippity | `gippity.db` | `chat_history`, `user_privacy`, `chat_attachments`, `chat_history_edits` | `gippity_chat_history`, `gippity_user_privacy`, `gippity_chat_attachments`, `gippity_chat_history_edits` |
| leetoclock | `plugins/leetoclock.sqlite` | `players`, `seasons`, `games`, `scores`, `highscores` | `leetoclock_players`, `leetoclock_seasons`, `leetoclock_games`, `leetoclock_scores`, `leetoclock_highscores` |
| coffee | `coffee.db` | `user_preferences`, `coffee_grabs` (plus later additions) | `coffee_user_beverage_preferences`, `coffee_user_greetings`, and other `coffee_*` tables |

The coffee schema changed the most across releases. Inspect `coffee.db` with `.schema` and map each table to its current `coffee_*` name; when in doubt, copy only the tables your data actually contains and let the bot create the rest.
