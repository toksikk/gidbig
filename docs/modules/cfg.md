# cfg

Directory `internal/cfg`. YAML config load + validation.

- `GetConfig()` – lazy singleton, calls `loadFile()` once.
- `loadFile()` – opens `config.yaml`, exits process on error.
- `decodeConfig()` – defaults + required-field validation.

Required: `discord.token`, non-empty `gippity.allowed_guilds`. `web.session_secret` required when `web.port != 0`. Defaults: soundboard queue depth 6, gippity reply rate limit 30/h, gippity classifier rate limit 30/h, gippity `reply_in_thread`/`respond_to_all_messages` false, gippity `tools.enabled` false, gippity `tools.max_iterations` 3.

## Load flow

```mermaid
sequenceDiagram
    participant Caller
    participant C as cfg
    participant FS as config.yaml

    Caller->>C: GetConfig()
    alt cached
        C-->>Caller: *Config
    else first call
        C->>FS: os.Open("config.yaml")
        C->>C: apply defaults
        C->>C: yaml.Decode
        alt validation fails
            C->>C: slog.Error + os.Exit(1)
        else valid
            C-->>Caller: *Config
        end
    end
```
