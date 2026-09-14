# core

Package `gidbig`, directory `internal/core`. Composition root.

- `cmd.go` – `StartGidbig`: builds session, wires modules, registers slash commands, waits for signal, shuts down.
- `soundboard.go` / `soundboard_init.go` – scan/load `audio/*.dca`, per-guild play queue.
- `slashcmd.go` – `/list`, `/uptime`, `/play`.
- `webserver.go` – optional OAuth session web UI, sound + eso APIs.
- `wsdeadline.go` – read/write deadlines on discordgo websockets.
- `version.go`, `discordlog.go`, `structs.go`.

## Startup sequence

```mermaid
sequenceDiagram
    participant main
    participant core as core.StartGidbig
    participant cfg
    participant dg as discordgo
    participant llm
    participant mod as Modules
    participant web as WebServer

    main->>core: StartGidbig()
    core->>cfg: GetConfig()
    core->>core: createCollections() + Load()
    core->>dg: New("Bot token") + Open()
    dg-->>core: READY
    core->>llm: Initialize + ResolvePersonality
    core->>mod: Init(Deps) for coffee/eso/gamerstatus/gippity/leetoclock/stoll/wttrin
    mod-->>core: listeners + background tasks
    core->>dg: ApplicationCommandBulkOverwrite(cmds)
    core->>web: go startWebServer() (if configured)
    core->>core: wait SIGINT/SIGTERM
    core->>mod: cancel ctx, Shutdown()
    core->>dg: Close()
```

## Sound playback

```mermaid
sequenceDiagram
    participant U as User
    participant core
    participant q as guild queue
    participant vc as VoiceConnection

    U->>core: /play collection sound
    core->>core: deferred respond
    core->>q: go enqueuePlay(play)
    alt no active queue
        core->>vc: ChannelVoiceJoin
        core->>vc: sleep 250ms (DAVE handshake)
        core->>vc: Speaking(true)
        core->>vc: send Opus frames (backpressure paced)
        core->>vc: Speaking(false)
        core->>core: play Next / drain queue
        core->>vc: Disconnect
    else queue occupies
        core->>q: push play
    end
```
