# Implementation Framework

How to build the notes backend. Read fully before writing any code. DESIGN.md
is the normative contract; this document is the working method. Where they
conflict, DESIGN.md wins and you flag the conflict in your final report.

## 1. Ground rules

- DESIGN.md is normative. Never improvise around a gap you find; implement
  what is written, and report the gap at the end.
- One vertical slice at a time: one seam, one failing test, one minimal
  implementation. Never all-tests-then-all-code.
- No speculative features. If DESIGN.md does not name it, do not build it.
- Common code is factored into one reusable function when the second caller
  appears, not before, and never copied into a third.
- Readable code wins over clever code. Every module is something a stranger
  can debug at 3 a.m. with only logs.

## 2. The loop (per slice)

1. RED: write one failing test at the seam for the behavior you are about to
   add. Run it. Watch it fail for the right reason.
2. GREEN: write the minimal code that passes it. Nothing more.
3. Run the slice's tests plus everything that exists so far. All green before
   you continue.
4. Commit (section 6).
5. Refactor only between slices, never mid-red or mid-green, and only with
   the suite green before and after.

## 3. Seams (the only places tests live)

- The HTTP API, driven through real HTTP requests against the real binary.
- The Note Store's public interface (create, read, append, edit, delete,
  dedup-check), against a temp directory.
- Each adapter's interface, against its stub (section 5).
- The chain runner, by planting notes with given processing states and
  draining them with stub adapters.

Never test private functions, never reach into the database or files from an
API-level test to assert state, never mock a module's own internals. A test
that breaks when the implementation changes but the behavior does not is a
bad test; rewrite it at the seam.

## 4. Test layout

```
tests/
  unit/            one file per module, fast, no I/O beyond temp dirs
  system/          end-to-end: real binary, temp notes dir, stub services
  stubs/           the stub services and PATH shims (section 5)
  fixtures/        small audio file, sample HTML page, sample VTT
```

System tests boot the real server binary with a config pointing at a temp
notes dir and the stubs, then drive curl flows. They assert on HTTP responses,
on the note files on disk, on git log output, and on logs. Each system test
gets its own temp dir, its own port, its own git repo. Tests run in any order
and in parallel.

Determinism rules: no sleeps. Wait on observable state with a bounded poll
(file exists, processing.step advanced, HTTP 200), and every poll has a
timeout that fails the test by name. No fixed ports (bind port 0, read the
port). No test reads or writes outside its temp dir. No live network: the
only HTTP a test may reach is a stub on 127.0.0.1.

## 5. Stub inventory

Build these first, in phase 1, and keep them honest:

- `stub_whisper`: local HTTP server. Accepts multipart POST, returns
  configurable JSON (success transcript, 500, 422, connection refusal,
  delayed response). Records requests for assertions.
- `stub_llm`: returns a configurable tag list, and a malformed-response mode.
- `stub_embed`: returns a deterministic fixed-size vector derived from the
  input text hash (so similarity assertions are stable).
- `shim_yt-dlp`: a PATH-injected executable that writes a fixture VTT for a
  known URL, exits 2 for "no subtitles", exits 3 for "cookies expired".
- `shim_extract`: a PATH-injected trafilatura helper emitting fixture JSON
  from a local HTML file.
- `null_git` (unit tests only): records commit calls, performs no I/O.

A stub that behaves differently from the real service without documenting the
difference is a defect. Note every simplification in the stub's README
comment.

## 6. Commit discipline

- One slice, one commit. Conventional subject: `feat:`, `fix:`, `test:`,
  `refactor:`, `chore:`.
- Green suite before every commit. No commit of known-red work except a
  failing RED test commit explicitly marked `test: RED <behavior>` when a
  slice must be parked mid-loop.
- Commit messages state behavior, not files: `feat: PATCH returns 409 on
  stale base_modified`.

## 7. Logging standard

Structured lines (JSON or key=value), one per state transition and one per
external call. Always include the note id where one exists, the module, and
the outcome. A stuck note must be diagnosable from logs alone: which step,
which status, what the adapter last said.

## 8. Definition of done (every phase)

1. All unit and system tests green, run in a clean checkout.
2. The phase card's acceptance script passes verbatim, by copy-pasting its
   commands.
3. Logs show the state transitions the script exercises.
4. `git log` in the notes repo of the system-test run shows one commit per
   content mutation and none for bookkeeping.
5. README updated: how to configure, run, and exercise the new behavior.
6. Report: what was built, what was verified (with command output), which
   gaps in DESIGN.md were found, what was deliberately not built.

## 9. When stuck

Stop. State the contract you cannot satisfy, the smallest change to DESIGN.md
that would resolve it, and two alternatives. Do not guess. A guess that
contradicts the design costs more than a pause.

## 10. Context discipline

At phase start read your task card fully, then the DESIGN.md sections the
card names, then this document. Do not read the whole design doc looking for
work; the card is the work.
