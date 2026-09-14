# gippity

Directory `internal/gippity`. Legacy module: wired via `gippity.Start`, not `bot.Module` (started directly in `core/cmd.go`).

LLM chat for Discord: answers when the bot is mentioned, keeps per-channel history, describes image attachments, honors per-user privacy, rate-limits mentions.

- Command: `/gippity privacy on|off`.
- Storage: SQLite `gippity.db` (`chat_history`, `chat_history_edits`, `chat_attachments`, privacy).
- Rate limit: `rate_limit_messages_per_hour` per user.

## Message answer flow

```mermaid
sequenceDiagram
    participant U as User
    participant D as Discord
    participant G as gippity
    participant DB as SQLite
    participant V as Vision LLM
    participant L as LLM

    U->>D: message (+ attachments)
    D->>G: onMessageCreate
    G->>DB: addMessageToDatabase
    opt image attachments
        G->>V: describeImages(urls)
        V-->>G: description
        G->>DB: addAttachmentsToDatabase
    end
    G->>G: limited()? bot / ignored / guild / mention rate limit
    alt allowed and mentioned
        G->>L: typing indicator
        G->>DB: getLastNMessagesFromDatabase(10)
        G->>G: build system prompt (personality, season, reactions, pseudonyms)
        G->>L: Chat.Completions.New
        L-->>G: answer
        G->>D: ChannelMessageSend(answer)
    end
```

Edited messages update `chat_history_edits` (versioned). Message replies inject a system note with the referenced message content.
