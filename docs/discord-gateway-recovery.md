# Discord gateway stalls

## Invalid session during resume (2026-09-15)

The last gateway messages were `Closing and reconnecting in response to Op7`,
Hello, Resume, `Closing and reconnecting in response to Op9`, and `called`.
The web server remained alive afterwards.

In the previous discordgo fork pin (`930441e7`), `Session.Open` holds the session
mutex while calling `onEvent` for the handshake response. If Discord rejects
Resume with Op9, `onEvent` calls `CloseWithCode`, which takes the same mutex.
This self-deadlock also blocks heartbeat handling, status updates and shutdown.
Socket deadlines cannot release a mutex. The same problem exists for Op7
received during the handshake.

## Upstream fixes and new pin

The fork is now pinned to **`94d3e03d65d14a4a6876ccfb973c0232d1a17b90`**
(`v0.0.0-20260913055947-94d3e03d65d1`). Keep an explicit commit rather than a
floating branch so every build uses the same reviewed and tested voice code.

- [PR #5](https://github.com/yeongaori/discordgo-fork/pull/5) moves Op7/Op9
  reconnect handling out of `onEvent`. During the handshake, `Open` now returns
  the reconnect error, cleans up and releases its lock before retrying.
- [PR #6](https://github.com/yeongaori/discordgo-fork/pull/6) restores immediate
  initial DAVE activation after Welcome via `ActivatePreparedTransition`.
  This addresses the silent-audio regression that required the old pin (#113).
- [6f7dfa36](https://github.com/yeongaori/discordgo-fork/commit/6f7dfa36be69de5b479a06380262c4fe28fad439)
  discards stale resume information after three failed reconnect attempts.
- [PR #10](https://github.com/yeongaori/discordgo-fork/pull/10) adds further
  DAVE readiness and encryption reliability improvements.

The local WebSocket regression test exercises READY → Op7 → Resume → Op9,
both resumable and non-resumable, and Op7 during Resume. It requires successful
reconnection, a new message dispatch and a real heartbeat ACK after recovery,
without a watchdog or process restart. Subprocess isolation bounds regressions
that would otherwise deadlock the whole test suite. Separate watchdog tests
cover stale heartbeats and a permanently held session lock.

The soundboard retains its current 250 ms pre-roll. The new Welcome activation
is automatic inside the fork. We do not add a call to `WaitForDAVEReady`: its
context cancellation broadcasts without holding `Cond.L`, leaving a potential
lost wakeup between checking `ctx.Err()` and `Cond.Wait()`. Adoption needs a
separate fix/test of that method rather than assuming its deadline is reliable.

### Live verification required before deployment

Automated gateway and DAVE transition tests cannot prove audible Discord
playback. **Live playback on the new pin is still pending**; `930441e7` remains
the previous live-verified baseline.

In a DAVE-enabled voice channel, verify a named sound and random sound after a
fresh join, then repeat after disconnect/rejoin (including a short sound whose
start would reveal handshake-related frame loss). Confirm both audible output
and `DAVE initial transition activated after Welcome canEncrypt=true` in logs.
Record the result here before deployment; a speaking indicator alone is not
evidence that Discord clients successfully decrypt the audio.

## Recovery and diagnostics

- A watchdog starts **before** the initial `Open`. Every 30 seconds it logs
  `Discord gateway health` with goroutine count, session-lock availability,
  and (when accessible) the last heartbeat ACK and its age in milliseconds.
- It uses `TryRLock`, so it can diagnose the lock even when Discord callbacks
  and background tasks are stuck. No chat activity is required for liveness.
- After five minutes of continuously unavailable session lock or stale ACK,
  it logs `Discord gateway stalled; exiting for supervisor restart`, followed
  by goroutine stacks (bounded to 1 MiB total), and exits with status **1**.
  It deliberately bypasses graceful cleanup that could acquire the stuck lock.
- **A process supervisor with restart-on-failure is required.** The provided
  Compose service uses `restart: unless-stopped`. For systemd, configure
  `Restart=on-failure`; other deployment methods need an equivalent policy.
  A Docker healthcheck alone does not restart an unhealthy container.
- Ordinary reconnect attempts have five minutes to recover. A longer network
  outage can also trigger a restart; the reason/ACK age distinguishes this
  from the session-lock stall. The fork update fixes the known invalid-session
  deadlock in place; the watchdog remains a safety net for future stalls.
- Normal shutdown cancels the watchdog. The 10-second shutdown deadline also
  covers background tasks and emits stacks if they cannot stop.

Library logs include source file/line and function, making `called` useful.
Handshake packets are summarized by opcode, event type and sequence instead
of dumping READY payloads. READY/RESUMED no longer report heartbeat latency:
the fork seeds ACK on Hello, so subtracting the previous or zero send time
produces misleading values. Disconnect callbacks are asynchronous and may
appear after a successful reconnect; they are events, not current health.

For a future incident, retain the last few minutes of `Discord gateway health`,
the library reconnect logs, the watchdog error and **all** associated stack
records, plus the supervisor's exit/restart messages.
