# leetoclock

Directory `internal/leetoclock`. Implements `bot.Module`. No slash commands.

Daily game: players race to post exactly when the clock hits `13:37` (configurable). Score = ms offset from target (can be negative). Keeps a scoreboard, reacts with award emojis, announces winners.

- Store: SQLite via `leetoclock/util/datastore`.
- Emojis: configurable guild IDs, else Unicode fallback.
- Debug mode: one minute after start, fast tick.

## Scoring flow

```mermaid
sequenceDiagram
    participant U as User
    participant D as Discord
    participant M as leetoclock.Module
    participant S as datastore
    participant Q as reaction worker

    U->>D: post message near 13:37
    D->>M: onMessageCreate
    M->>M: beginHandler (accepting?)
    M->>M: timestamp from snowflake in target window?
    M->>S: EnsureSeason / EnsureGame / EnsurePlayer
    M->>S: CreateScore(messageID, score = ts-target)
    opt exactly on target minute
        M->>Q: react :alarm_clock: (once per user)
    end
    M->>M: go renewGame()
    M->>S: GetScoresForGameID
    M->>M: build scoreboard (winners/zonks/early birds)
    M->>Q: add/remove award reactions
```

Background loops: `runPreparationLoop` announces the target time; `runWinnerLoop` posts the scoreboard after the round and resets state.
