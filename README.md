# Gidbig 🤖

Gidbig is a Discord bot written in Go — soundboard playback in voice channels, AI chat, weather, time-based games, and more.

> **⚠️ Fair warning:** This project has evolved into an AI agent adventure park and LLM testing ground. Features have been written, reviewed, refactored, and occasionally broken by a rotating cast of AI models. The fact that it's still running is either a testament to Go's resilience or proof that LLMs are, at the very least, okayish at writing code. Possibly both. 🎢🦾

## ✨ Features

### Core

- 🏓 **Ping/Pong** — send exact lowercase `ping` or `pong`; the bot replies `Pong!` or `Ping!` and updates its game status
- 🔊 **Soundboard** — plays pre-encoded `.dca` audio files in your voice channel
  - `/play <collection> [sound]` — play a specific sound, or a random one from the collection when `sound` is omitted
  - `/list` — list all available sound collections
  - Files live in `audio/` as `{prefix}_{soundname}.dca`; an optional matching `.txt` file supplies the Web UI description
- 🌐 **Web UI** — browser interface to trigger sounds; requires Discord OAuth2 credentials in config
  - `/admin` — owner-only panel for the runtime settings, the same keys and rules as `/admin config`; a non-owner gets a 404, and the login must be less than 12h old
- 📊 **`/status`** — owner-only ephemeral command showing versions, uptime, memory/runtime statistics, and guild/user counts
- ⏱️ **`/uptime`** — owner-only uptime command
- 🛡️ **`/admin`** — owner-only administrative commands

### 🔌 Modules

| Plugin | What it does |
|---|---|
| 🛡️ **anticheat** | `/anticheat query:<game or vendor>` — searches the GamingOnLinux Linux/Steam Deck anti-cheat compatibility list (works/broken, Proton and native Linux status, notes) |
| ☕ **coffee** | Greets users with their preferred morning beverage when they say "moin", "morgen", etc. `/setbeverage` configures it, `/brew` serves a selected drink, and `/coffeemachine` manages and reports machine state |
| 🔮 **eso** | `/eso [thema]` generates esoteric pseudoscience nonsense through the LLM, with a local fallback |
| 🎮 **gamerstatus** | Rotates the bot's Discord game/activity status every 5–15 minutes after an initial 5-minute delay |
| 🤖 **gippity** | Responds through an LLM when mentioned in an allowed guild, stores conversation history in SQLite, and provides `/gippity privacy set:on\|off` and `/gippity nonmention set:on\|off` |
| 🕐 **leetoclock** | Daily 13:37 game — messages around 13:37 score by time offset; the top three at or after 13:37 rank alongside early/late categories. `/leetoclock top`, `/leetoclock daily` and `/leetoclock player` show records |
| 🧌 **stoll** | `/stoll` — Stoll-related commands |
| 🐧 **wardogs** | `/wardogs` — reports whether WARDOGS officially supports Linux/Proton, read from the community tracker doeswardogshavelinux.support (10 min cache) |
| 🌤️ **wttrin** | `/wttr location:<location>` / `/wttrf location:<location>` — current weather / forecast with an LLM-generated outro |
| 🌦️ **openmeteo** | `/weather location:<location> [forecast] [days]` — Open-Meteo weather and up to 16-day forecast (also the LLM `weather` tool) |
| 🎬 **tmdb** | `/wheretostream title:<title> [region:XX]` — where a movie or series streams (subscription, free, with ads) via TMDB/JustWatch; pick the match from a select menu (also the LLM `streaming_providers` tool). Optional: enabled only when `tmdb.read_access_token` is set |
| 📺 **tv** | `/tv now [channel]`, `/tv channel <channel> [day]`, `/tv search <title>` — TV program (running/next show, day schedule, title search) from the free-epg.de feeds; by default ~27 main German channels, other countries configurable, cached on disk and refreshed every 6 h (also the LLM `tv_program` tool). Optional: enabled only when `tv.enabled` is true |

## 🚀 Quickstart

### 1. Configure

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml`:

```yaml
discord:
    token: "YOUR_DISCORD_BOT_TOKEN"
    owner_id: "YOUR_DISCORD_USER_ID"
    shard_id: 0
    shard_count: 0
web:
    oauth:
        client_id: "YOUR_OAUTH_CLIENT_ID"
        client_secret: "YOUR_OAUTH_CLIENT_SECRET"
        redirect_uri: "YOUR_REDIRECT_URI"
    session_secret: "A_STRONG_RANDOM_SESSION_SECRET"
    port: 8080
gippity:
    allowed_guilds:
        - "YOUR_DISCORD_GUILD_ID"
    ignored_users: []
    reply_in_thread: false # answer mentions in a thread instead of the channel
    respond_to_all_messages: false # let the classifier decide on non-mention messages
llm:
    provider: "openai"
    model: "gpt-4o-mini"
    vision_model: ""
    personality: ""
    personality_preset: ""
dev_mode: true
```

Set `OPENAI_API_KEY` in the bot environment for the default OpenAI provider, and `OPENROUTER_API_KEY` when using OpenRouter. The environment variable takes precedence; `llm.api_key` is the fallback so a single `config.yaml` can also carry the credential.

OpenRouter is also supported through its OpenAI-compatible API:

```yaml
llm:
    provider: "openrouter"
    model: "anthropic/claude-sonnet-4"
    vision_model: "google/gemini-2.5-flash" # optional; defaults to model
    http_referer: "https://your-deployment.example" # optional attribution
    title: "Gidbig" # optional attribution
    api_key: "" # optional; OPENROUTER_API_KEY wins when set
```

Set `OPENROUTER_API_KEY` when using OpenRouter. `llm.provider` defaults to `openai`, the OpenAI model defaults to `gpt-4o-mini`, and `llm.vision_model` defaults to `llm.model`. `llm.base_url` can optionally override either provider's API endpoint for an OpenAI-compatible gateway.

The web server starts only when `web.port`, `web.session_secret`, `web.oauth.client_id`, `web.oauth.client_secret`, and `web.oauth.redirect_uri` are set. `gippity.allowed_guilds` restricts guilds where mention-driven AI chat runs. With `gippity.reply_in_thread: true` the bot answers mentions inside a public thread started on the mentioning message instead of the channel itself (requires the Create Public Threads permission; messages already posted in a thread are answered there). With `gippity.respond_to_all_messages: true` a classifier (`gippity.classifier_model`, defaults to `llm.model`) decides per non-mention message whether to answer; each user must also opt in with `/gippity nonmention set:on`, and `gippity.classifier_rate_limit_per_hour` caps classifier calls per user per hour.

The optional `tmdb` module powers `/wheretostream` and the LLM `streaming_providers` tool:

```yaml
tmdb:
    read_access_token: "YOUR_TMDB_API_READ_ACCESS_TOKEN"
    region: DE # optional, default DE (two-letter country code)
    language: de-DE # optional, default de-DE
```

Use the **API Read Access Token** from themoviedb.org → Settings → API, not the short "API Key". An empty or missing token disables the module (no command, no tool); a token change needs a restart. `tmdb.region` and `tmdb.language` can also be changed at runtime with `/admin config`. TMDB's free tier is for non-commercial use; the bot credits JustWatch and TMDB as required.

The optional `tv` module powers `/tv` and the LLM `tv_program` tool:

```yaml
tv:
    enabled: true
    # regions: [de, at] # optional free-epg.de country feeds, default [de]
    # channels: ["DasErste.de", "ORF1.at"] # optional XMLTV IDs; replaces the built-in list, required for regions other than de
    # timezone: Europe/Berlin # optional IANA zone for times and day boundaries
    # cache_dir: cache # optional, default cache (relative to the working directory)
    # refresh_interval: 6h # optional, default 6h, minimum 1h
```

Each region is downloaded from `https://www.free-epg.de/api/epg/<region>.xml.gz`; the built-in channel list only covers Germany, so any other region needs `tv.channels` (pick the IDs from the region's feed). Each feed is cached in `cache_dir` and reused on restart while it is less than 24 h old, so keep that directory on persistent storage to avoid a download per deploy. All `tv.*` keys are read at boot: `/admin config` lists them read-only and a change needs a restart.

### 2. Add audio files 🎵

Drop `.dca` files into `./audio/` following the naming scheme `{prefix}_{soundname}.dca`. Prefix and sound name must be nonempty and cannot contain underscores.
Example: `airhorn_default.dca` → `/play airhorn default`

### 3. Build and run 🔨

Local builds require Go 1.26 and a CGO-capable C compiler for SQLite.

```bash
make build
./bin/gidbig
```

Run the binary from a working directory containing `config.yaml`, `audio/`, and `web/`. It writes the shared SQLite database to `gidbig.db` there.

## 🛠️ Build

```bash
make build                    # Build binary → ./bin/gidbig
make test                     # go test -v ./...
make lint                     # golangci-lint run ./...
make release                  # Cross-compile linux/{amd64,arm64,386,arm} and darwin/amd64 into ./bin/release
make docker                   # Build Docker image
make update                   # go get -u -t ./... && go mod tidy
```

### CI and deployments

The `Check and build` workflow runs on pushes, pull requests targeting `master`
(including forks), and manual starts. Go/build-input and CI-configuration changes
run lint, tests, and a build; manual starts always run those checks. Pull-request
runs test GitHub's merge ref with a read-only token and no deployment secrets.

Only code pushes in `toksikk/gidbig` can dispatch deployments after successful
checks: `master`, or an internal branch with an open, non-draft PR. Renovate and
Dependabot actors/branches remain excluded. Fork PRs, pull-request events, manual
starts, tags, and CI-only changes never deploy.

## 🐳 Docker

```bash
make docker

# Prepare writable persistent storage
touch data/gidbig.db

# Run with Web UI on localhost:8080 (put a TLS reverse proxy in front)
docker run -it \
  -p 127.0.0.1:8080:8080 \
  --mount type=bind,source=$(pwd)/config.yaml,target=/gidbig/config.yaml,readonly \
  --mount type=bind,source=$(pwd)/audio,target=/gidbig/audio \
  --mount type=bind,source=$(pwd)/data/gidbig.db,target=/gidbig/gidbig.db \
  gidbig:$(git describe --tags)
```

The database file is fixed at `gidbig.db` in the bot's working directory. All modules share it. Upgrading from an older release that used per-module files (`coffee.db`, `plugins/leetoclock.sqlite`, `gippity.db`) requires a one-time data migration, see [docs/database-migration.md](docs/database-migration.md). The Docker mount above places the file at the expected path. `docker-compose.yml` is also available, but add equivalent writable persistence mounts before using it in production.

## 📄 License

See [LICENSE](LICENSE).
