# Adding an LLM tool

Tools let the gippity answer path consult internal data while composing a reply.
A tool is a name, a description, a JSON Schema for its arguments, and a handler.
The registry is provider-agnostic and lives in `internal/tool`; it imports only
the standard library, so any module can register tools without an import cycle.

## How it works

- `internal/core/cmd.go` creates one `tool.NewRegistry()` and puts it in
  `bot.Deps.Tools`. Every module receives it in `Init`.
- Modules register their own tools in `Init` (for example
  `gippity.registerSearchHistoryTool`, `core.registerTools`,
  `soundboard.registerTools` and `coffee.registerTools`). Registration order does
  not matter: the registry is mutable and the answer loop reads `All()` at
  request time.
- `gippity` runs a bounded tool loop in `completeWithTools`
  (`internal/gippity/gippity_tools.go`) when `gippity.tools.enabled` is true.
  Otherwise the answer path is the unchanged single completion call.

## Steps

1. Add the `Def` and register it from the owning module's `Init`:

   ```go
   m.tools.Register(tool.Def{
       Name:        "my_tool",
       Description: "What the tool returns, so the model knows when to call it.",
       Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
       Handler:     m.myToolHandler,
   })
   ```

2. Implement the handler. It receives the caller scope in `tool.Env` and the
   raw JSON arguments. Return one already-formatted string; the loop truncates it.

   ```go
   func (m *Module) myToolHandler(ctx context.Context, env tool.Env, args json.RawMessage) (string, error) {
       var in struct {
           Q string `json:"q"`
       }
       if err := json.Unmarshal(args, &in); err != nil {
           return "", err
       }
       // Clamp every lookup to env.ChannelID / env.GuildID / env.UserID.
       return "result", nil
   }
   ```

3. Test the handler directly. The tool loop itself is unit-tested with a
   `chatCompletionFunc` fake that returns one tool call and then a final answer
   (see `internal/gippity/gippity_tools_test.go`).

No change to `internal/gippity` or `internal/core` is needed to add a tool.

## Registered tools

| Tool | Module | What it returns |
| --- | --- | --- |
| `bot_status` | core | per-module runtime counters (same as `/status`) |
| `search_chat_history` | gippity | keyword matches in stored Discord history, scoped to the caller's channel or guild |
| `get_channel_members` | gippity | members who can see the caller's channel; replaces the always-on member line in the system prompt |
| `list_sounds` | soundboard | sound collections and the sound names in each |
| `coffee_status` | coffee | the current guild's coffee machine levels and leaderboards |
| `coffee_stats` | coffee | one user's drinks, refills and grounds stats in the current guild |
| `leetoclock_records` | leetoclock | a top-players board or one player's records for the current guild, or globally |
| `anticheat_lookup` | anticheat | a game/vendor match from the cached GamingOnLinux anti-cheat list |
| `wardogs_status` | wardogs | the cached WARDOGS Linux/Proton support verdict |
| `weather` | wttrin | current weather or forecast for a location, from the cached wttr.in lookup |
| `stoll_quote` | stoll | a random Dr. Axel Stoll quote |

## Configuration

```yaml
gippity:
  tools:
    enabled: false        # master switch, default off
    max_iterations: 3     # tool-call rounds before a final tool-free answer
    allowed: []           # optional allowlist of tool names; empty = all
    reasoning_effort: ""  # only sent when tools are attached; "" omits it
```

`reasoning_effort` is applied to requests that carry tools and cleared for the
final tool-free call, so that call can reason again. Some providers forbid tools
together with reasoning and require `reasoning_effort: "none"` (for example
OpenAI `gpt-5.6-luna` on `/v1/chat/completions`). Accepted values: `none`,
`minimal`, `low`, `medium`, `high`, `xhigh`, `max`, or empty to omit.

## Invariants

- **Read-only.** A tool must never write, send, react or moderate.
- **Caller scope, not bot permissions.** Clamp lookups to `tool.Env`; never
  trust a channel, guild or user ID from model arguments.
- **Privacy parity.** A tool that reads `gippity_chat_history` must honour
  `gippity_user_privacy` exactly like the prompt path.
- **Untrusted data.** Stored text is data, not instructions. Frame it as data
  and keep results short.
- **No arbitrary fetches.** A tool may read the owning module's own fixed,
  sanitized source through its existing cache and rate limiter (the anticheat
  CSV, wttr.in weather, the wardogs tracker). Never fetch a model-supplied URL
  or host (SSRF); the destination is always a module constant.
