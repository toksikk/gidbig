# admin

Directory `internal/admin`. Implements `bot.Module`; constructed with `admin.New(providers ...bot.AdminProvider)`.

Owner-only `/admin` command. The command tree is built from the injected `bot.AdminProvider` modules (coffee, gippity) plus the built-in `info` subcommand.

- Providers are passed to `admin.New` by the composition root (`internal/core/cmd.go`), including only modules whose `Init` succeeded.
- Adding a provider means passing it to `admin.New`; no edits inside `internal/admin` are required.
- `SetInfoText` injects the `/admin info` renderer, owned by the composition root because it aggregates the shared status snapshot.
- Owner authorization runs through the `bot.OwnerOnly` middleware; there is no inline owner check.
- All responses are ephemeral.

## Dispatch flow

```mermaid
sequenceDiagram
    participant O as Owner
    participant D as Discord
    participant A as admin
    participant P as AdminProvider (coffee, gippity)

    O->>D: /admin <group> <sub>
    D->>A: onAdminInteractionCreate
    A->>A: filter to /admin application command
    A->>A: OwnerOnly middleware
    alt not owner
        A-->>O: Access denied.
    else owner
        A->>D: defer ephemeral
        alt info
            A-->>O: bot stats block
        else provider group
            A->>P: HandleAdminSubcommand(sub)
            P-->>O: result
        end
    end
```
