# Modules

One file per internal package. Each doc: what it does + sequence diagram of the key flow.

## Feature modules

| Module | Slash commands | Purpose |
| --- | --- | --- |
| [core](core.md) | `/status` `/uptime` | Composition root, web server, Discord lifecycle |
| [soundboard](soundboard.md) | `/list` `/play` | Audio collection loading, per-guild voice playback queue |
| [coffee](coffee.md) | `/brew` `/coffeemachine` `/setbeverage` | Coffee machine economy, orders, stats |
| [gippity](gippity.md) | `/gippity` | LLM chat over Discord messages, vision, privacy, optional thread replies, opt-in non-mention classifier |
| [leetoclock](leetoclock.md) | `/leetoclock` | Daily reaction-time game + scoreboard |
| [wttrin](wttrin.md) | `/wttr` `/wttrf` | Weather + forecast via wttr.in + LLM outro |
| [openmeteo](openmeteo.md) | `/weather` | Weather + up to 16-day forecast via Open-Meteo; owns the LLM `weather` tool |
| [openholidays](openholidays.md) | `/holidays` | Public and school holidays via the OpenHolidays API; owns the LLM `holidays` tool |
| [eso](eso.md) | `/eso` | Esoteric nonsense generator (LLM, fallback) |
| [stoll](stoll.md) | `/stoll` | Random Dr. Axel Stoll quote |
| [anticheat](anticheat.md) | `/anticheat` | GamingOnLinux anti-cheat compatibility lookup (cached CSV) |
| [tmdb](tmdb.md) | `/wheretostream` | Optional: where a movie/series streams via TMDB/JustWatch; owns the LLM `streaming_providers` tool |
| [tv](tv.md) | `/tv` | Optional: TV program (now/next, day schedule, title search) from free-epg.de country feeds, German by default; owns the LLM `tv_program` tool |
| [wardogs](wardogs.md) | `/wardogs` | WARDOGS Linux/Proton support check via doeswardogshavelinux.support |
| [trivia](trivia.md) | – | Counts chat stats (messages, words, GIFs, pictures, links, reactions) for users with `/gippity privacy` off and posts random LLM-phrased trivia drop-ins |
| [gamerstatus](gamerstatus.md) | – | Rotating bot game status |
| [admin](admin.md) | `/admin` | Owner-only admin dispatch, aggregates providers |
| [configcmd](configcmd.md) | `/admin config` | Owner-only runtime settings: list/get/set/unset/add/remove hot keys; cold keys read-only |
| [logscmd](logscmd.md) | `/admin logs show` | Owner-only recent log lines from the in-memory window (also `/admin/logs` web page) |

## Shared infra

| Module | Purpose |
| --- | --- |
| [bot](bot.md) | Module interface, router, background supervisor, middleware |
| [cfg](cfg.md) | YAML config load + validation |
| [shareddb](shareddb.md) | Shared `gidbig.db` open helper (WAL + busy timeout) |
| [settings](settings.md) | Runtime setting overrides in the shared database (`Deps.Settings`) |
| [llm](llm.md) | OpenAI/OpenRouter client, personalities, language detect |
| [tool](tool.md) | Provider-agnostic LLM tool registry (`Deps.Tools`) |
| [logwindow](logwindow.md) | In-memory ring of recent log records; slog tee + shared line renderer |
| [util](util.md) | Discord helpers, AI responder, reactions, season logic |
