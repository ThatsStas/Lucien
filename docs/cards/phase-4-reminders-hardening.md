# Task Card — Phase 4: Reminders, GC, push, hardening

Read order: this card, `../IMPLEMENTATION-FRAMEWORK.md`, then `../DESIGN.md`
sections 3.2 (remind_at, defer_until), 5.4, 5.10, 10, 11.

Prerequisite: phase 3 done and green; read its final report first.

## Goal

The time dimension works end to end, deleted notes expire, the git remote is
pushed to, and the whole system survives a documented chaos pass.

## Build

1. **Reminder/defer filters finalized**: `remind_due`, `defer_due` per
   DESIGN.md 5.4, including the state exclusions. PATCH clearing
   `remind_at` (ack) verified.
2. **Trash GC**: a periodic sweep (config interval) removing `.trash/` files
   older than `trash_retention_days`, one commit per removal
   (`gc <id>`), index cleanup. Never touches git history.
3. **Push**: timer (or documented host cron) running
   `git -C <notes_dir> push` every `push_interval_minutes` (default 15),
   plus on clean shutdown. Push failure: log, retry next interval, never
   block writes (DESIGN.md 10).
4. **Chaos pass**, scripted and repeatable: kill -9 mid-transcription, kill
   mid-PATCH, backend down with 50 pending notes, whisper flapping
   (alternating 500/200), full disk simulation on a tmpfs. Each scenario has
   an expected recovery per DESIGN.md 11; the script asserts it.
5. **Hardening sweep**: every error path returns the documented status and
   body shape; every log line needed to diagnose a stuck note exists (note
   id, step, status, adapter outcome).

## Not in this phase

Anything client-facing new. This phase is operability.

## Acceptance script (sketch, tighten at phase start)

```bash
# remind_due returns only notes past remind_at, excludes done/archived
# defer_due likewise; PATCH clearing remind_at removes it from remind_due
# a note older than retention in .trash disappears after a GC sweep, with
#   a "gc <id>" commit, and remains recoverable via git history
# push: with a local bare remote configured, commits appear there within
#   the interval; with the remote removed, writes still succeed
# chaos script: all five scenarios recover per DESIGN.md 11
```

## Definition of done

Framework 8, plus the chaos script committed under `tests/system/` so it can
be re-run after any future change.
