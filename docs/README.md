# Notes Backend — Handoff Bundle

Self-hosted REST backend for GTD-style notes. Design session 2026-11-07.

## What is here

- `DESIGN.md` — the normative design: architecture, schema, state machines,
  per-endpoint contracts, failure modes. Reference document, written to be
  grepped by section, not read end to end.
- `IMPLEMENTATION-FRAMEWORK.md` — the working method: TDD loop, seams, stub
  services, commit discipline, definition of done.
- `cards/` — one task card per phase. The card is the entry point you hand
  to the implementing model; it names the reading order and carries the
  acceptance script for its phase.

## How to use

Start one implementation session per phase, in a fresh context. Point the
model at exactly one file: `cards/phase-1-mvp.md` for the first session. The
card tells the model what else to read and in what order. Do not paste
DESIGN.md into the prompt; the model reads the sections the card names.

Each phase ends with the definition-of-done report from
IMPLEMENTATION-FRAMEWORK.md section 8. Read that report before starting the
next phase: it lists the design gaps the phase found, and later cards may
need amendments from it.

The implementation language is deliberately not chosen in the design. Pick
it at the start of phase 1 (C++ with drogon or Python with FastAPI were the
candidates in the design session; the contracts are language-independent).
