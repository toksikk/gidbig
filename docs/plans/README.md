# Implementation plans

This directory holds the implementation plans produced by the `gidbig-planner`
agent and consumed by `gidbig-dev`.

A plan is **transient branch scaffolding, not documentation**. It is committed so
the work is reviewable against a written intent, and it is removed again before
the branch merges, so `master` never carries it. The directory itself stays
tracked because this file lives here.

## Lifecycle

1. **Planner writes the plan, uncommitted.** `gidbig-planner` writes exactly one
   file, `docs/plans/<name>.md`, and commits nothing. At this point git does not
   track it: `git status` shows it as untracked and the working tree is otherwise
   clean of the change under discussion.
2. **Dev commits the plan first.** The first commit on the feature branch adds
   that file and nothing else:

   ```
   plans (#317): add the implementation plan for runtime config
   ```

   Every later commit is implementation, tests, or docs. Committing the plan
   first means the diff for the code that follows is readable against the plan
   it implements.
3. **Review and implementation proceed normally.** The plan file is expected
   context for `gidbig-reviewer`; its presence is not a finding.
4. **The plan commit is dropped immediately before the merge.** See below.

## Name

`docs/plans/<issue>-<slug>.md`, for example `docs/plans/317-runtime-config.md`.
Without an issue, use the branch slug: `docs/plans/<slug>.md`. The file is the
plan the planner returned, verbatim — decisions, steps, tests, docs, risks.

## Dropping the plan commit before merge

Immediately before the merge, drop the first commit with an interactive rebase, but
**only when both conditions hold**:

- the commit touches nothing outside `docs/plans/`, and
- the plan file appears in exactly one commit on the branch:

  ```bash
  git rev-list --count origin/master..HEAD -- docs/plans/<name>.md
  ```

  `1` means the plan was never revised later; anything higher means dropping only
  the first commit would leave the file behind.

If either check fails, **do not drop anything**. Report which check failed and
ask: a plan file mixed into a code commit, or revised across commits, would
otherwise land on `master` with the code.

Dropping rewrites already-published branch history, so it needs a force-push to
the branch — never to `master` — and only after the user confirms. The result is
that the merged branch carries only the real change, and the plan disappears with
the dropped commit; nothing needs to be deleted by hand.

## Why not just keep the plan

A plan is a snapshot of intent at one moment. Once the code exists, the code and
`docs/modules/<pkg>.md` are the record; a stale plan that still reads as
authoritative is worse than no plan. Keeping the intent for review, then dropping
it at merge, gets both.
