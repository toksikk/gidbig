# gamerstatus

Directory `internal/gamerstatus`. Implements `bot.Module`.

Rotates the bot's "playing" status through a retro-game list. On seasonal dates (`util.Season`) shows a themed status instead. No commands.

## Background rotation

```mermaid
sequenceDiagram
    participant Sup as BackgroundSupervisor
    participant M as gamerstatus.Module
    participant D as Discord

    Sup->>M: runStatusLoop(ctx)
    M->>M: wait initialDelay (5 min)
    loop until ctx cancelled
        M->>D: UpdateStreamingStatus(clear)
        M->>M: currentGame() (seasonal or random)
        M->>D: UpdateGameStatus(game)
        M->>M: wait random 5–15 min
    end
```

Seasonal overrides: Halloween, April Fools, New Year, Easter, Christmas; otherwise random game from `games`.
