# eso

Directory `internal/eso`. Implements `bot.Module`.

Generates one sentence of esoteric German nonsense via LLM with few-shot examples. Falls back to a deterministic generator (`buildMessage`) when the LLM fails or returns empty.

- Command: `/eso [thema]`.
- Also exposed over HTTP: `GET/POST /api/eso` (web server), via `Module.GenerateText`.
- Core helper: `util.AIResponder`.

## Command flow

```mermaid
sequenceDiagram
    participant U as User
    participant D as Discord
    participant E as eso.Module
    participant R as util.AIResponder
    participant L as LLM

    U->>D: /eso thema
    D->>E: onInteractionCreate
    E->>D: deferred response
    E->>E: ResolveMentionsWithRestore(thema)
    E->>E: go generate
    E->>R: GenerateWithPrompt(prompt)
    R->>R: pick random example pool entries
    R->>L: GenerateMessage(system, user)
    alt LLM ok and non-empty
        L-->>R: sentence
    else error / empty
        R->>R: Fallback() = buildMessage()
    end
    R-->>E: text
    E->>E: restore mention tokens
    E->>D: InteractionResponseEdit(text)
    E->>D: react 🧠
```
