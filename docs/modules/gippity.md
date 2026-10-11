# gippity

Directory `internal/gippity`. Implements `bot.Module`, `bot.AdminProvider` and `bot.StatsProvider`; registered with `bot.Bot` in `core/cmd.go` (which calls `Init`).

LLM chat for Discord: answers when the bot is mentioned, keeps per-channel history, describes image attachments, honors per-user privacy, rate-limits mentions, and can optionally answer non-mention messages that the classifier accepts.

- Commands: `/gippity privacy set:on|off`, `/gippity nonmention set:on|off`.
- Storage: shared SQLite `gidbig.db`, prefixed tables (`gippity_chat_history`, `gippity_chat_history_edits`, `gippity_chat_attachments`, `gippity_user_privacy`, `gippity_user_consent`).
- Rate limit: `rate_limit_messages_per_hour` per user (reply budget). Non-mention classifier calls have a separate `classifier_rate_limit_per_hour` per-user budget.
- Reply target: `reply_in_thread` (default `false`) posts the answer inside a public thread started on the mentioning message instead of the channel. The thread name is derived from that message (mentions, backticks and control/format characters stripped, clamped to 100 characters). Messages that already arrive in a thread are answered in place, since Discord does not nest threads, and a failed thread start falls back to the channel. Rate-limit notices follow the same setting.
- Debug channel: `debug_channel` (default empty) restricts the bot to one channel while debug logging is enabled, so a debug run does not answer in every channel. Empty disables the restriction and the bot answers in all channels.
- Non-mention responses: `respond_to_all_messages` (default `false`) enables the classifier decision for messages that do not mention the bot. Each user must additionally opt in with `/gippity nonmention set:on` (`gippity_user_consent`, default off). Direct mentions always bypass the classifier and the consent check. The decision uses `classifier_model` (empty → `llm.model`) with `temperature=0` and a 5-token cap, parses a strict `YES`/`NO`, and fails closed on any provider or parsing error. Vision/attachment descriptions run only after a positive decision, and over-budget non-mentions are skipped silently. The classifier prompt names the bot, so a message that addresses it by name without a Discord mention (`gustaff, ...`) can be accepted; the names are the bot's own Discord username and display name plus any `classifier_names` aliases, and the model still decides per message.
- Tool calling: `gippity.tools` (see [tools](../tools.md)) optionally offers registered tools to the model and runs a bounded tool loop in `completeWithTools` (`gippity_tools.go`). `enabled` (default `false`) keeps the pre-tool single-call path; `max_iterations` (default 3) caps tool-call rounds before a final tool-free call; `allowed` optionally restricts tool names; `reasoning_effort` is sent only on tool-bearing requests and cleared for the final tool-free call (some providers forbid tools together with reasoning). The tool scope (`tool.Env{GuildID, ChannelID, UserID}`) is taken from the originating message, never from model arguments, and the whole loop is bounded by a 60s context deadline. gippity owns `search_chat_history` (scope-clamped channel/guild history search by keyword and/or an inclusive `from`/`to` `YYYY-MM-DD` date range, with prompt-path privacy parity) and `get_channel_members` (the members who can see the caller's channel; when the tool is offered the member list is dropped from the system prompt, and when it is not — tools disabled, no registry, or allowlisted out — the list stays in the prompt). The core module owns `bot_status`.

## Message answer flow

```mermaid
sequenceDiagram
    participant U as User
    participant D as Discord
    participant G as gippity
    participant DB as SQLite
    participant V as Vision LLM
    participant C as Classifier LLM
    participant L as LLM
    participant T as Tool

    U->>D: message (+ attachments)
    D->>G: onMessageCreate
    G->>DB: addMessageToDatabase
    G->>G: eligible()? author/bot/ignored/guild/message type
    alt mentioned
        G->>G: isLimitedUser? (reply budget)
        opt over reply budget
            G->>D: rate-limit notice
        end
    else not mentioned
        G->>G: respond_to_all_messages and user consent?
        G->>G: isClassifierLimited? (classifier budget)
        G->>C: Chat.Completions.New(temperature=0, YES/NO)
        C-->>G: YES or NO (fail closed)
    end
    opt answered and image attachments
        G->>V: describeImages(urls)
        V-->>G: description
        G->>DB: addAttachmentsToDatabase
    end
    alt answered
        G->>L: typing indicator
        G->>DB: getLastNMessagesFromDatabase(10)
        G->>G: build system prompt (personality, season, reactions, pseudonyms)
        loop bounded by max_iterations (tools enabled)
            G->>L: Chat.Completions.New (tools, tool_choice=auto)
            L-->>G: tool_calls or answer
            opt tool_calls returned
                G->>T: run tool in caller scope
                T-->>G: truncated result
            end
        end
        alt reply_in_thread and channel is not a thread
            G->>D: MessageThreadStartComplex(name from message)
            D-->>G: thread
            G->>D: ChannelMessageSend(thread.id, answer)
        else channel reply
            G->>D: ChannelMessageSend(channel.id, answer)
        end
    end
```

Edited messages update `chat_history_edits` (versioned). Message replies inject a system note with the referenced message content.
