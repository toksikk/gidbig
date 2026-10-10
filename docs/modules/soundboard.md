# soundboard

Directory `internal/soundboard`. Implements `bot.Module` and `bot.StatsProvider`.

Scans `audio/*.dca` into per-prefix collections and plays them in voice channels, one queue per guild.

- Commands: `/list`, `/play collection [sound]`.
- Lifecycle: `Init` loads the `.dca` files and applies `soundboard.queue_max_depth`; `Shutdown` has nothing to release (voice connections close when a queue drains).
- Exported API for the web server: `Collections()`, `Enqueue(user, guild, command, soundname)`, `QueueStatus()`.
- Stats: sound/collection/queue counters in `/status`.

## Playback flow

```mermaid
sequenceDiagram
    participant U as User
    participant D as Discord
    participant S as soundboard.Module
    participant q as guild queue
    participant vc as VoiceConnection

    U->>D: /play collection sound
    D->>S: onInteractionCreate
    S->>S: deferInteraction
    S->>S: resolve collection + sound (random if omitted)
    S->>q: enqueue (goroutine)
    alt no active queue
        S->>vc: ChannelVoiceJoin
        S->>vc: sleep 250ms (DAVE handshake)
        S->>vc: Speaking(true)
        S->>vc: send Opus frames (backpressure paced)
        S->>vc: Speaking(false)
        S->>S: play Next / drain queue
        S->>vc: Disconnect
    else queue occupies
        S->>q: push play
    end
```
