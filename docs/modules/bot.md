# bot

Directory `internal/bot`. Shared module framework. Not user-facing.

- `module.go` – `Module` interface (`Name`, `Init`, `Commands`, `Listeners`, `Components`, `Background`, `Shutdown`) and `AdminProvider`.
- `deps.go` – `Deps` (Session, Config, LLM, Logger, OwnerID).
- `router.go` – command/component dispatch with middleware. No-op until modules migrate.
- `bot.go` – `Bot` wiring Router + supervisor.
- `background.go` – `BackgroundSupervisor` runs panic-safe goroutines.
- `middleware.go` – `OwnerOnly`, `RateLimit`, `Recover`, `WithCorrelationID`.

## Interaction dispatch

```mermaid
sequenceDiagram
    participant D as Discord
    participant R as Router
    participant MW as middleware chain
    participant H as command handler

    D->>R: onInteractionCreate
    alt application command
        R->>R: lookup command name
        R->>MW: build chain (reversed)
        MW->>MW: OwnerOnly / RateLimit / Recover / CorrelationID
        MW->>H: handler(session, interaction)
    else message component
        R->>R: match customID prefix
        R->>H: component handler
    end
```

`BackgroundSupervisor.Start` launches each task in a goroutine and recovers panics; `Wait` blocks until all return.
