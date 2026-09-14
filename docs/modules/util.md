# util

Directory `internal/util`. Shared helpers. Not user-facing.

- `AIResponder` – LLM generation with few-shot examples + deterministic fallback (used by eso, coffee).
- `discordhelper.go` – channel/guild names, member lists, mention resolve/restore, user lookup, emoji reactions queue.
- `season.go` – `Season` detection, Easter computus (Meeus-Jones-Butcher).
- `util.go` – `RandomRange`, emoji byte constants.

## AIResponder generation

```mermaid
sequenceDiagram
    participant Caller
    participant R as AIResponder
    participant L as LLM

    Caller->>R: GenerateWithPrompt(userPrompt)
    alt GenerateFn nil
        R-->>Caller: Fallback()
    else configured
        R->>R: pickExamples(pool, ExampleCount)
        R->>R: inject {{examples}} into template
        R->>L: GenerateFn(ctx, system, user)
        alt error or empty
            R->>R: OnFallback(err)
            R-->>Caller: Fallback()
        else ok
            R-->>Caller: trimmed text
        end
    end
```

`ReactOnMessage` enqueues onto a single worker goroutine; `ResolveMentionsWithRestore` returns a restore func mapping display names back to `<@id>` tokens.
