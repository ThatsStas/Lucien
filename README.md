# Lucien — notes backend

Self-hosted REST backend for GTD-style notes. Notes are self-contained JSON
files in a git repository; slow work (transcription, extraction, tagging,
embedding) runs in an in-process worker. The normative design lives in
[`docs/DESIGN.md`](docs/DESIGN.md); the working method in
[`docs/IMPLEMENTATION-FRAMEWORK.md`](docs/IMPLEMENTATION-FRAMEWORK.md);
per-phase work in [`docs/cards/`](docs/cards/).

## Status

Phase 1 (MVP) in progress: schema, Note Store + git, auth, notes CRUD, index
filters, worker with the transcription pipeline.

## Build & test

```sh
go build ./...
go test ./...
```

## Configure

Copy `config.example.toml`, fill `clients[].token_sha256` (SHA-256 hex of the
bearer token you hand to each client) and point `notes_dir` at your notes git
repository. The backend commits every content mutation to that repository.
