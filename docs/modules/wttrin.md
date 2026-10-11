# wttrin

Directory `internal/wttrin`. Implements `bot.Module`.

Commands `/wttr` (current) and `/wttrf` (multi-day forecast). Fetches JSON from `wttr.in`, formats an emoji table, appends an LLM-written one-line outro in the channel language.

- Cache: 10 min TTL per location, single-flight dedup.
- LLM: injected via `Deps.LLM` (`*llm.Client`), see `internal/llm`.
- LLM tool: `weather` returns the cached current conditions or forecast; it skips the LLM outro.

## Weather flow

```mermaid
sequenceDiagram
    participant U as User
    participant D as Discord
    participant W as wttrin.Module
    participant Cache as cache
    participant API as wttr.in
    participant L as LLM

    U->>D: /wttr location
    D->>W: onInteractionCreate
    W->>D: deferred response
    W->>W: go composeWeather(forecast)
    W->>Cache: getWeatherCached(location)
    alt fresh cache
        Cache-->>W: cached response
    else miss
        W->>Cache: register inflight (single-flight)
        W->>API: GET https://wttr.in/<loc>?format=j1
        API-->>W: JSON
        W->>Cache: store TTL 10m
    end
    W->>W: buildWeatherString / buildForecastString
    W->>L: DetectChannelLanguage + one-sentence outro
    L-->>W: outro (fallback: none)
    W->>D: InteractionResponseEdit(structured + outro)
```
