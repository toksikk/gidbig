# react

Directory `internal/react`. Implements `bot.Module` and `bot.StatsProvider`.

`/react` lets anyone pick a custom emoji from an ephemeral, paginated select
menu. The bot reacts to the target message with it. Once the invoker adds the
same emoji to that message, the bot withdraws its own reaction, so only the
invoker's remains and it looks as if the invoker reacted. The watch is consumed
on that hand-off. Other users' reactions are ignored.

- Command: `/react` with exactly one of `user` (that user's newest message among
  the last 20 in the channel) or `message_id` (must be in the current channel),
  plus optional `filter` (case-insensitive emoji name substring). All replies are
  ephemeral.
- Emojis: every available custom emoji in the gateway state cache across the
  bot's guilds, deduplicated by ID, sorted by name; 25 per page with Prev/Next.
- Picker sessions: in memory, keyed by a random 8-byte hex token in the custom
  IDs (`react:sel:<tok>`, `react:prev|next:<tok>:<page>`), 15 min TTL, swept by
  the `react-session-sweeper` background task. Only the invoker may use them.
- Watches: in memory (lost on restart), at most 1000, oldest evicted first,
  one-shot. A watch is added only after the bot's own reaction succeeds.
- Permissions: Add Reactions, Read Message History; no Manage Messages (the bot only
  removes its own reaction; a failure is logged as a warning).
- Stats: `react_watches` (pending), `react_removed` (bot reactions withdrawn).

## Flow

```mermaid
sequenceDiagram
    participant U as Invoker
    participant D as Discord
    participant R as react.Module

    U->>D: /react user:@x [filter]
    D->>R: handleCommand
    R->>D: DeferReply (ephemeral)
    R->>D: ChannelMessages(channel, 20)
    R->>R: findLastMessage, collectEmojis, store picker session
    R->>D: EditReply(select menu page 0)
    U->>D: Prev/Next
    D->>R: handleComponent → UpdateMessage(page n)
    U->>D: select emoji
    D->>R: handleComponent
    R->>D: MessageReactionAdd(name:id)
    R->>R: watches.add(message, emoji, invoker)
    R->>D: UpdateMessage(confirmation, no components)
    U->>D: adds same emoji
    D->>R: onReactionAdd
    R->>R: watches.take (one-shot)
    R->>D: MessageReactionRemove(@me)
```
