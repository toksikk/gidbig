---
name: Grill Me
description: >-
  Interrogation protocol for planning and design sessions. Load before writing
  any plan — plan mode, the gidbig-planner agent, "design a change", "scope
  this", "plan X", "how would you change Y" — to question the human, one
  decision at a time, on every point that needs a human decision and is not
  provable from the repository, so that no ambiguity survives into
  implementation. Do not load for routine coding, review, or execution turns.
---

# Grill Me — no ambiguity leaves planning

Planning fails in one of two ways: the plan is technically right but solves the
wrong thing, or it is right in outline and full of silent assumptions that
surface only during implementation. This skill attacks both. The planner's job
is not to produce a document; it is to extract every human decision the change
depends on, get it made explicitly, and record it.

**The human owns decisions. You own engineering.** Ask about the first, decide
the second, and never confuse them.

## Hard rules

1. **Investigate before asking.** Anything the repository can answer is not a
   question. Read the code, the issue *and all its comments*, the docs, the git
   log, and the tests first, and cite `file:line` as evidence. Coming back with a
   question the code already answers burns trust.
2. **Classify every unknown.**
   - *Human decision* → ask, do not proceed without an answer: goal and why,
     scope in/out, non-goals, audience and permissions, privacy/consent and
     retention, user-visible names and wording, defaults, accepted trade-offs,
     breaking-change tolerance, rollout and who verifies live, delivery split,
     deadlines, ownership after ship.
   - *Engineering choice* → decide yourself, then state it in the plan so it can
     be challenged: SQL shape, helper placement, sort stability, lock scope,
     log levels, test technique, error wrapping.
   If you are unsure which class something is, it is a human decision.
3. **One decision per question.** Batch at most four *independent* questions in a
   single `question` call. Ask strictly one at a time when the answer changes
   which options are available for the next question.
4. **Give real options, never open-ended prompting.** Two to four concrete
   choices, each `description` stating the consequence, not the implementation.
   Put the recommended one first and suffix its label with `(Recommended)`.
   Never add a "type your own answer" option — the tool adds it.
5. **Silence is not approval.** If the question is dismissed, aborted, or
   answered off-topic, do not infer an answer and do not proceed on a guess.
   Re-ask once, differently, or record the item as blocked.
6. **"Whatever you think is best" is a request for a proposal, not a decision.**
   Propose exactly one concrete option with its rationale, then ask for explicit
   confirmation. Do not treat the proposal as accepted.
7. **"I don't know" gets options, then a deferral.** Offer two or three workable
   options with their costs and your default. If there is still no decision,
   record it as `deferred — <item>, owner <who>, revisit <when>` and mark the
   plan blocked on that item rather than quietly choosing.
8. **Turn vague answers into falsifiable statements.** "Keep it simple", "make it
   fast", "do it properly", "good UX" are not requirements. Restate as a
   testable sentence — "one request per server per hour, rejected with an
   ephemeral error" — and confirm it.
9. **Surface conflicts with repo hard rules instead of complying silently.** If
   an answer breaks `CLAUDE.md` or `AGENTS.md` (commit format, version labels,
   shared-DB rules, pinned `discordgo`/DAVE, slash-command rules), name the rule
   and offer the compliant alternative.
10. **Never ask twice.** Keep a decision register and read it before every new
    question. A decided item stays decided until the human changes it.
11. **The plan is not finished while a human decision is unresolved.** The final
    plan's open-questions section must be empty, or contain only items the human
    explicitly deferred (with owner and reason).
12. **End with a read-back.** Present the decision register and ask whether
    anything is still ambiguous. Planning is done only after that confirmation.

## Phase 0 — the essence, before scope and before design

Get the *why* nailed down first, in the human's words, and confirm it back. A
plan with the wrong essence is worthless no matter how precise the steps are.

- What user-visible problem does this solve, and for whom?
- Why now, and what happens if we do nothing?
- How will we know it worked — what observable signal, seen by whom?
- Restate it in one sentence and ask: "This is the change — correct?"

Then re-read the originating issue **including every comment**. Issue comments in
this repository carry status audits, prerequisite maps, and scope corrections
that silently invalidate the description.

## Phase 1 — the boundary

- What is in scope, and what is explicitly out (name the tempting adjacent work
  and get it excluded)?
- Non-goals: what must this change *not* do?
- Acceptance: the observable condition that proves it is done, and who checks it.
- Must-have versus nice-to-have; anything that can be dropped if it grows.
- Deadline, trigger, or event that makes this urgent.

## Phase 2 — the grill list

Walk every domain below. For each, end with one of: a decision, an explicit "not
applicable because …", or a deferral with an owner. Silence is not an outcome.

1. **Audience and permissions** — who may trigger or see it, owner-only gating,
   ephemeral versus public replies, consent, privacy, retention and deletion.
2. **UX and naming** — command, subcommand and option names, defaults, response
   wording, error text, ordering, tie-breaks, and what the user sees on failure.
3. **Data, persistence and migration** — new tables/columns/keys, uniqueness and
   idempotency, whether the schema change can be avoided entirely, backfill and
   repair of existing or broken historical rows, reversibility, and rollback.
4. **Precedence and configuration** — config file versus database versus built-in
   default, hot-apply versus restart-required, secrets handling, new optional
   config with a safe zero-value default.
5. **Failure and edges** — partial failure, retries, duplicate processing,
   timeouts, rate limits, degraded/offline behavior, at-most-once versus
   at-least-once, and whether failures are logged only or also user-visible.
6. **Compatibility** — the pinned `discordgo`/DAVE constraint, other modules, the
   existing command surface, the web API, and released versions.
7. **Ops and rollout** — feature gate, deploy order, who verifies live and how,
   what to watch, and what would indicate a regression.
8. **Delivery** — PR split into independently reversible changes, version
   labels, commit scopes, docs to update, test scope, and the definition of done.
9. **Trade-offs the human owns** — cost, maintenance burden, who owns the code
   after ship, and tolerance for user-visible breakage.

## Phase 3 — write the plan

Include, in this order:

- **Goal and essence** — one sentence plus the confirmed acceptance signal.
- **Evidence** — the files and commands that establish current behavior.
- **Decisions (human-made)** — every item from the register, with rationale and
  who decided it. Decisions are the plan's spine; steps hang off them.
- **My engineering choices** — calls you made, stated so they can be challenged.
- **Assumed unless objected** — anything you are proceeding on that the human has
  not explicitly blessed, each with its consequence if wrong.
- **Deliberately deferred** — item, reason, owner, revisit trigger.
- **Open questions** — must be empty. If it is not, the plan is not ready.
- **Acceptance signals** — how the change will be observed to work.

## Phase 4 — the final sweep

1. Read the decision register back to the human and ask for confirmation.
2. Re-read the plan and hunt for soft language: *should, might, probably, TBD,
   maybe, as needed, we'll see, ideally, if time permits*. Each one is an
   unresolved decision. Convert it into a decision or delete it.
3. If any step cannot be executed without a further answer, the plan is not
   finished — go ask.
4. Dismissed or unanswered questions leave the plan blocked, never resolved.

## Using the question tool

```jsonc
{
  "questions": [
    {
      "header": "Short label",              // max 30 characters
      "question": "One decision, fully self-contained, with the trade-off.",
      "options": [
        { "label": "Option A (Recommended)", "description": "Consequence of choosing A." },
        { "label": "Option B",               "description": "Consequence of choosing B." }
      ],
      "multiple": true                       // only when choices are not exclusive
    }
  ]
}
```

- Every option's `description` is about consequences, cost, or risk — not about
  how you would implement it.
- `multiple: true` only for genuinely non-exclusive choices (for example, which
  of several documents to update).
- Do not bundle unrelated questions to save turns; batch only what the human can
  answer in one breath without needing an earlier answer.

## Anti-patterns

| Anti-pattern | Do instead |
|---|---|
| Draft the plan, then ask for approval | Grill first, then draft |
| Ten questions in one dump | ≤ 4 independent, sequential when dependent |
| Accepting "make it good/simple/clean" | Force a measurable criterion |
| Asking what the code already answers | Investigate, cite `file:line` |
| Treating your technical pick as a user decision | Decide it, label it, move on |
| Treating a user preference as your call | Ask, do not assume |
| Recording an assumption as a decision | Label it "assumed unless objected" |
| Letting a dismissed question stand in for approval | Re-ask or mark blocked |
| Asking the same thing twice | Check the decision register |

## Repo triggers (gidbig)

- Anything that moves, rewrites, or repairs data → read
  `docs/database-migration.md` and ask about backfill, historical/broken rows,
  reversibility and rollback before proposing a schema change.
- Anything adding a command, config key, `/admin` surface, or stats → ask about
  naming, visibility/gating, and the end-to-end check.
- Anything touching the `discordgo` pin → ask who runs the live DAVE playback
  check and when.
- Anything user-visible → ask how the PR splits and which version label each PR
  carries.
