# Gidbig 🤖

Gidbig is a Discord bot written in Go — soundboard playback in voice channels, AI chat, weather, time-based games, and more.

> **⚠️ Fair warning:** This project has evolved into an AI agent adventure park and LLM testing ground. Features have been written, reviewed, refactored, and occasionally broken by a rotating cast of AI models. The fact that it's still running is either a testament to Go's resilience or proof that LLMs are, at the very least, okayish at writing code. Possibly both. 🎢🦾

## ✨ Features

### Core

- 🏓 **Ping/Pong** — type `ping` → bot replies `Pong!` (and vice versa)
- 🔊 **Soundboard** — plays pre-encoded `.dca` audio files in your voice channel
  - `!<prefix>` — play a random sound from that collection
  - `!<prefix> <soundname>` — play a specific sound
  - `!list` — list all available sound collections
  - `!uptime` — show bot uptime
  - Files live in `audio/` as `{prefix}_{soundname}.dca`; optional `.txt` file with the same name adds a description
- 🌐 **Web UI** — browser interface to trigger sounds; requires Discord OAuth2 credentials in config
- 📊 **`/status`** — slash command showing bot version and uptime

### 🔌 Built-in plugins

| Plugin | What it does |
|---|---|
| ☕ **coffee** | Greets users with their preferred morning beverage when they say "moin", "hallo", etc. `/setbeverage <emoji>` to configure, `/brew` to trigger manually |
| 🗡️ **eso** | `!eso` — Elder Scrolls Online utility commands |
| 🎮 **gamerstatus** | Rotates the bot's Discord custom status periodically |
| 🤖 **gippity** | AI chat via `/gippity`; backed by an LLM, stores conversation history in SQLite; restricted to configured guild IDs |
| 🕐 **leetoclock** | Daily 13:37 game — first to post in the channel wins; scores by reaction time; posts a scoreboard at end of game |
| 🧌 **stoll** | `!stoll` — Stoll-related commands |
| 🌤️ **wttrin** | `!wttr <location>` / `!wttrf <location>` — current weather / forecast with an LLM-generated outro |

### 🧩 Dynamic plugins

Gidbig loads `.so` plugin files from `./plugins/` at startup. A plugin must export `Start(*discordgo.Session)`, `PluginName string`, and `PluginVersion string`.

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
    session_secret: "base64-encoded-32-random-bytes"
    port: 8080
gippity:
    allowed_guilds:
        - "YOUR_DISCORD_GUILD_ID"
    ignored_users: []
dev_mode: true
```

The web server only starts when `web.port`, `web.oauth.client_id`, and `web.oauth.client_secret` are all set. `gippity.allowed_guilds` restricts which servers can use `/gippity`.

### 2. Add audio files 🎵

Drop `.dca` files into `./audio/` following the naming scheme `{prefix}_{soundname}.dca`.  
Example: `airhorn_default.dca` → `!airhorn default`

### 3. Build and run 🔨

```bash
make build
./bin/gidbig
```

## 🛠️ Build

```bash
make build                    # Build binary → ./bin/gidbig
make test                     # go test -v ./...
make lint                     # golangci-lint run ./...
make release                  # Cross-compile: linux/amd64, arm64, 386, arm and darwin/amd64
make docker                   # Build Docker image
make update                   # go get -u -t ./... && go mod tidy
make build_with_local_plugins # Build with local plugin path replacements (see Makefile)
```

## 🐳 Docker

```bash
make docker

# Run with mounted config and audio directory
docker run -it \
  --mount type=bind,source=$(pwd)/config.yaml,target=/gidbig/config.yaml \
  --mount type=bind,source=$(pwd)/audio,target=/gidbig/audio \
  gidbig:$(git describe --tags)
```

Or use `docker-compose.yml` in the repo root.

## 🗺️ Roadmap

- 🔀 **Migrate all `!`-prefix commands to Discord slash commands** — soundboard, `!wttr`, `!stoll`, `!eso`, etc.
- 🗑️ **Remove dynamic plugin system** — retire the `.so` loader; consolidate everything into built-in plugins
- 🏗️ **Refactor architecture** — move from the current event-handler-per-plugin pattern toward a cleaner command/handler abstraction

## 📄 License

See [LICENSE](LICENSE).
