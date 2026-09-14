# admin

Directory `internal/admin`. Not a `bot.Module`; started via `admin.Start`.

Owner-only `/admin` command. Dispatches to built-in subcommands (`info`, `gippity privacy|history`) and to registered `bot.AdminProvider` modules (e.g. coffee).

- `RegisterProvider` must run before `Start` (command tree is built from providers).
- All responses ephemeral.

## Dispatch flow

```mermaid
sequenceDiagram
    participant O as Owner
    participant D as Discord
    participant A as admin
    participant P as AdminProvider (e.g. coffee)
    participant G as gippity

    O->>D: /admin <group> <sub>
    D->>A: onAdminInteractionCreate
    A->>A: callerID == ownerID?
    alt not owner
        A-->>O: Access denied.
    else owner
        A->>D: defer ephemeral
        alt info
            A-->>O: bot stats block
        else gippity
            A->>G: AdminGetUserPrivacy / AdminGetUsersWithHistory
            G-->>A: data
            A-->>O: result
        else provider group
            A->>P: HandleAdminSubcommand(sub)
            P-->>O: result
        end
    end
```
