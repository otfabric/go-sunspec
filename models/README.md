# SunSpec JSON models

This directory holds a local copy of the official SunSpec information-model
definitions used to generate [`registry/models_gen.go`](../registry/models_gen.go).

## Upstream source

| | |
|---|---|
| Repository | [github.com/sunspec/models](https://github.com/sunspec/models) |
| Path | [`json/`](https://github.com/sunspec/models/tree/master/json) on branch `master` |
| Sync script | [`../sync-models.sh`](../sync-models.sh) |
| License | [Apache License 2.0](https://github.com/sunspec/models/blob/master/LICENSE) |

Files are downloaded as-is from upstream (for example `json/model_1.json` →
`model_1.json`). Do not hand-edit these JSON files; change upstream or re-sync.

`schema.json` is the JSON Schema for the model documents and is also taken from
that upstream tree.

## Regenerating the Go registry

```bash
./sync-models.sh   # refresh models/ from upstream
make generate      # rewrite registry/models_gen.go
```

`make generate` alone regenerates from the JSON already present in this
directory without contacting GitHub.
