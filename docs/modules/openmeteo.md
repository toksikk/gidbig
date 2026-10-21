# openmeteo

Directory `internal/openmeteo`. Implements `bot.Module` and `bot.StatsProvider`.

Command `/weather location:<name> [forecast:<bool>] [days:<1-16>]` and the LLM tool `weather`, both backed by [Open-Meteo](https://open-meteo.com). No API key and no config. `/weather` posts publicly without an LLM outro; every reply ends with the attribution line `Data: Open-Meteo.com (CC BY 4.0)`. Open-Meteo's free API is licensed for non-commercial use.

- Geocoding: `geocoding-api.open-meteo.com/v1/search` with `count=10&language=de`; the first `country_code=DE` result wins, otherwise the first result. The resolved place (name, admin1, country) is always shown. No results → ``Location not found: `X` `` (slash) or `no location found for "<loc>"` (tool string).
- Forecast: one `api.open-meteo.com/v1/forecast` request with `timezone=auto`, `forecast_days=N`, `current`, `hourly` and `daily` variables (daily precipitation sum, max precipitation probability, sunshine duration, dominant wind direction, gusts, UV, sunrise/sunset).
- Days: 1–16, default 7; out-of-range values are clamped. Current conditions fetch one day.
- Cache: forecasts 10 min TTL keyed by `lat,lon` rounded to 2 decimals + days; geocodes 24 h TTL. Both single-flight, max 256 entries (expired entries swept when full; errors not cached).
- Fetch: shared single-flight fetches run under the module context (cancelled by `Shutdown`, which also waits for pending `/weather` replies), not the first caller's; 15 s HTTP timeout, non-200 is an error, bodies capped at 1 MiB.
- `/weather` with an empty or overlong location gets an ephemeral hint.

## `/weather` output

Formatted like wttrin (`/wttr`, `/wttrf`): H2 location heading with a Google Maps link, values in code blocks, English labels. The ⚠️ line lists rain probability > 50 %, gusts ≥ 50 km/h, thunderstorms (⛈️) and snow (❄️; the snow amount line appears only in the ≤7-day blocks); it is omitted when nothing applies. Rain and snow lines appear only when non-zero.

Current conditions (`/weather location:Winterberg`):

````
## 📍 [Winterberg, Deutschland (Nordrhein-Westfalen)](<https://www.google.com/maps/place/51.19,8.53>)
```
🌡️ 6°C (feels like 3°C)
💧 90% humidity
🌬️ ↙️ 14km/h
🌦️ Light rain
📅 Today: 7°C / 3°C, 🌧️ 3.3mm (63%), ☀️ 3.2h
⚠️ 🌧️ 63%
```
-# Data: Open-Meteo.com (CC BY 4.0)
````

Forecast up to 7 days: one `### 📅` heading and code block per day:

````
### 📅 Thu 2026-10-22
```
🌡️ 7°C / 3°C
🌬️ ↙️ 18km/h (gusts 41km/h)
🌦️ Light rain
🌧️ 3.3mm (63%)
☀️ 3.2h sun
⚠️ 🌧️ 63%
```
````

Forecast 8–16 days: one compact table, one line per day (emojis at the end keep columns aligned):

````
```
Day             °C     Rain    %    Sun     Wind
Thu 10-22      7/3    3.3mm  63%   3.2h   18km/h ↙️ 🌦️
Fri 10-23      9/2    0.0mm   5%   6.8h   12km/h ⬅️ ⛅
```
````

## LLM tool `weather`

Arguments: `location` (required, ≤100 characters), optional `latitude`/`longitude` (both or neither; then `location` is only a label and geocoding is skipped), `days`, `forecast`. Plain-text output: location line, current conditions, one line per day, and a 3-hour breakdown (8 slots, precipitation summed over the slot, max probability) for days 1–3. Without `forecast` only today is listed. Open-Meteo failures return an error; there is no wttr.in fallback.

## Weather flow

```mermaid
sequenceDiagram
    participant U as User / LLM
    participant M as openmeteo.Module
    participant GC as geocode cache
    participant FC as forecast cache
    participant G as Open-Meteo geocoding
    participant F as Open-Meteo forecast

    U->>M: /weather or weather tool
    alt latitude/longitude given (tool)
        M->>M: place = label + coordinates
    else
        M->>GC: get(lower(location))
        GC->>G: search?name=..&language=de (miss, single-flight)
        G-->>GC: results → prefer country_code DE
        GC-->>M: place or not found
    end
    M->>FC: get(lat,lon rounded, days)
    FC->>F: forecast?current&hourly&daily (miss, single-flight)
    F-->>FC: JSON (TTL 10m)
    FC-->>M: forecast
    M-->>U: tool text / Discord message + attribution
```
