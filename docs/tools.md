# Adding an LLM tool

Tools let the gippity answer path consult internal data while composing a reply.
A tool is a name, a description, a JSON Schema for its arguments, and a handler.
The registry is provider-agnostic and lives in `internal/tool`; it imports only
the standard library, so any module can register tools without an import cycle.

## How it works

- `internal/core/cmd.go` creates one `tool.NewRegistry()` and puts it in
  `bot.Deps.Tools`. Every module receives it in `Init`.
- Modules register their own tools in `Init` (see `gippity.registerSearchHistoryTool`
  and `core.registerTools`). Registration order does not matter: the registry is
  mutable and the answer loop reads `All()` at request time.
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
- **No external tools.** This framework is for internal data only; no web
  fetches or SSRF-prone calls.
