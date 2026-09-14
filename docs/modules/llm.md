# llm

Directory `internal/llm`. Shared OpenAI-compatible client + persona. Not user-facing.

- `Initialize` – builds client from `provider` (`openai`/`openrouter`), reads `OPENAI_API_KEY`/`OPENROUTER_API_KEY`, sets base URL, timeout, models.
- `GenerateMessage` / `GenerateMessageWith` – single-turn completion, 30s timeout, 150 max tokens.
- `DetectChannelLanguage` – LLM language detect over last 20 channel messages, 1h cache.
- `Personality` / `ResolvePersonality` – custom string > preset (`hal`, `schemer`, `dry`, `genalpha`) > built-in default.
- `Model`, `VisionModel`, `GetClient`.

## Client init + generation

```mermaid
sequenceDiagram
    participant Core as core.StartGidbig
    participant L as llm
    participant Env as environment
    participant API as Provider API
    participant Mod as Module

    Core->>L: Initialize(provider, model, ...)
    L->>Env: read OPENAI_API_KEY / OPENROUTER_API_KEY
    alt key missing / bad provider / bad base URL
        L-->>Core: error (fatal)
    else ok
        L->>L: openai.NewClient(opts)
        L-->>Core: nil
    end
    Core->>L: ResolvePersonality(custom, preset)
    Mod->>L: GenerateMessage(ctx, system, user)
    L->>L: WithTimeout(30s)
    L->>API: Chat.Completions.New
    API-->>L: completion
    L-->>Mod: content (or error)
```
