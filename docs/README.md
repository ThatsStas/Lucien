# Lucien: Handoff Bundle

Self-hosted REST backend for GTD-style notes, plus its web frontend. Backend
design session 2026-11-07; frontend design session 2026-11-08.

## What is here

- `DESIGN.md`, the normative design: architecture, schema, state machines,
  per-endpoint contracts, failure modes. Reference document, written to be
  grepped by section, not read end to end.
- `IMPLEMENTATION-FRAMEWORK.md`, the working method: TDD loop, seams, stub
  services, commit discipline, definition of done.
- `cards/`, one task card per phase. The card is the entry point you hand
  to the implementing model; it names the reading order and carries the
  acceptance script for its phase.
- `FRONTEND-DESIGN.md`, the normative design of the web frontend: the
  HTTP contract it builds against (verified against the implemented API),
  the decisions ledger, the three-layer rule, keymap, Element Plus
  containment rules, test infrastructure.
- `cards/web-1…web-6`, the frontend build, one card per face. Each card is
  self-contained: the model reads only that card plus FRONTEND-DESIGN.md and
  IMPLEMENTATION-FRAMEWORK.md, never the later cards.

## How to use (frontend)

Same discipline as the backend: one fresh implementation session per card,
in order, pointed at exactly one file (`cards/web-1-scaffold-settings-client.md`
first). A card ends green, unit and e2e suites pass, work committed, before
the next starts.

## How to use (backend)

Start one implementation session per phase, in a fresh context. Point the
model at exactly one file: `cards/phase-1-mvp.md` for the first session. The
card tells the model what else to read and in what order. Do not paste
DESIGN.md into the prompt; the model reads the sections the card names.

Each phase ends with the definition-of-done report from
IMPLEMENTATION-FRAMEWORK.md section 8. Read that report before starting the
next phase: it lists the design gaps the phase found, and later cards may
need amendments from it.

The backend is implemented in Go (chosen at phase 1 start; the contracts
were language-independent, this is not one). The frontend stack is fixed by
FRONTEND-DESIGN.md section 2 and not open for re-litigation per card.
