# SunSpec JSON models

This directory holds a local copy of the official SunSpec information-model
definitions used to generate [`registry/models_gen.go`](../registry/models_gen.go).

## Upstream source

| | |
|---|---|
| Repository | [github.com/sunspec/models](https://github.com/sunspec/models) |
| Path | [`json/`](https://github.com/sunspec/models/tree/master/json) on branch `master` |
| Commit | `90b4a331dcca1d6eac69c1bead952fddcc5852e0` (2026-08-20) |
| Retrieved | 2026-10-09 |
| Sync script | [`../sync-models.sh`](../sync-models.sh) |
| License | Apache License 2.0: [`LICENSE`](LICENSE), the file of that commit. Upstream has no `NOTICE` file. |
| Checksums | [`SHA256SUMS`](SHA256SUMS): the SHA-256 of `LICENSE` and of every JSON file |

Files are downloaded as-is from upstream (for example `json/model_1.json` →
`model_1.json`). Do not hand-edit these JSON files; change upstream or re-sync.

`schema.json` is the JSON Schema for the model documents and is also taken from
that upstream tree.

`sync-models.sh` takes every file from one commit, the head of `master` when
it runs, checks each against the git blob hash upstream lists for it, writes
`SHA256SUMS` and puts the commit and the date in the table above.

## Verifying

```bash
make models-verify   # models/ against SHA256SUMS
```

It fails when a listed file is changed or missing, and when the directory
holds a file that `SHA256SUMS` does not list (other than this README). CI runs
it next to `make generate-check`, so a model file that was edited by hand does
not reach the registry unnoticed. To compare with upstream instead, run
`./check-models.sh` (`make models-check`).

## Regenerating the Go registry

```bash
./sync-models.sh   # refresh models/ from upstream
make generate      # rewrite registry/models_gen.go
```

`make generate` alone regenerates from the JSON already present in this
directory without contacting GitHub.
