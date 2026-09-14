# coffee

Directory `internal/coffee`. Implements `bot.Module` + `bot.AdminProvider`.

Coffee machine economy per guild: beans, water, milk, grounds, tea bags. Tracks drink events, refills, grounds emptied, slackers, pickup violations.

- Commands: `/brew` (+ interactive menu), `/coffeemachine refill|empty|status|stats`, `/setbeverage`.
- Components: drink menu, Take-cup button (opener-only).
- Persistence: GORM/SQLite (`store.go`, `machine.go`).
- Background: `runOrderExpiry` expires unclaimed ready drinks.

## Brew + pickup flow

```mermaid
sequenceDiagram
    participant U as User
    participant D as Discord
    participant C as coffee.Module
    participant DB as SQLite
    participant L as LLM

    U->>D: /brew drink milk sugar
    D->>C: interactionCreate
    C->>C: deferInteraction (3s deadline)
    C->>DB: restrictionForUser
    alt blocked
        C-->>U: cannot brew until <t:...>
    else allowed
        C->>DB: dispense(): check stock, deduct, create DrinkEvent + DrinkOrder(brewing)
        DB-->>C: outcome
        C->>L: generate "brewing…" text (fallback if error)
        C-->>U: brewing status + <t:...:R> countdown
        C->>C: sleep brewTime(drink)
        C->>DB: markOrderReady(): status=ready, expiresAt=+20min
        C-->>U: "ready" + Take-cup button
        U->>D: press Take cup
        D->>C: handleTakeCupComponent
        C->>DB: pickupOrder(): status=picked_up
        C-->>U: "grabbed it" confirmation
    end
```

Unclaimed ready drinks expire via the background sweep; expiry records a `PickupViolation`, enough violations set a `BrewRestriction`.
