# go-sunspec Releases

## v0.4.1

**Date:** 2026-10-09
**Previous release:** v0.4.0

## Summary

Patch release: sync the SunSpec model definitions with upstream. One model changed: the units of a point in model 122. This is the first update found by the weekly models check added in v0.4.0 ([#1](https://github.com/otfabric/go-sunspec/issues/1)). No API changes.

## Changes

### Changed

- **Model 122 (Measurements_Status)** — the units of **`WAval`** (available watts) are now **`W`**; upstream changed them from `var`. `DecodedPoint.Units` for this point changes from `"var"` to `"W"`, and so does the unit `sunspecctl` prints next to it. The point's type, size, scale factor (`WAval_SF`) and value are unchanged.
- **`models/model_122.json`** — refreshed from [sunspec/models](https://github.com/sunspec/models) `master` with `./sync-models.sh`; still 112 compiled models.
- **`registry/models_gen.go`** — regenerated with `make generate`.

### Unchanged

- No other model differs from upstream (`./check-models.sh` reports the local copy in sync), and there are no API, wire or behaviour changes. Import path remains `github.com/otfabric/go-sunspec`.

---

## v0.4.0

**Date:** 2026-10-09
**Previous release:** v0.3.1

## Summary

Models are now decoded as the full tree of groups their definition describes, which fixes the DER curve and trip models that were decoded incompletely; this changes the shape of the decoded model and of the registry (see **Breaking changes**). `sunspecctl` can now reach SunSpec devices on serial lines with non-default settings, the repository watches the official SunSpec model definitions for changes every week, and the project gets the same quality gates as the rest of the otfabric library family: full doc comments, a much larger test suite, and stricter lint, formatting and coverage checks. Built on go-modbus v1.2.1.

## Breaking changes

There are API changes in this release. The module is pre-1.0 and the changes are needed to represent SunSpec models correctly.

- **Decoded models are a group tree.** `DecodedModel.FixedBlock` and `DecodedModel.RepeatingBlocks` are replaced by `DecodedModel.Group`, a `*DecodedGroup` with the model's own `Points` and, in `Groups`, the instances of the groups nested inside it, to any depth. `DecodedBlock` is renamed `DecodedGroup`; it gains `Name` and `Groups`, and `GroupIndex` becomes `Index` (0 for the top-level group, 1..N for nested instances).
  - `dm.FixedBlock.Points` → `dm.Group.Points`
  - `dm.RepeatingBlocks` → `dm.Group.Groups` (or `dm.Group.GroupsNamed("module")`)
  - New helpers: `DecodedModel.Point`, `DecodedGroup.Point`, `FindPoint`, `GroupsNamed`, `Walk`.
- **The registry is a group tree.** `ModelMeta.FixedBlock` and `ModelMeta.RepeatingBlock` are replaced by `ModelMeta.Group`. `GroupMeta` gains `Desc`, `Type`, `Count`, `CountPoint` and `Groups`; its `Repeating` field is now the method `Repeating()`. `ModelMeta.RepeatingLength()` is removed; `FixedLength()` remains. New helpers `GroupMeta.Group` and `GroupMeta.Point`.
- **`DecodedPoint.RegisterOffset`** is now the offset from the model's ID register (the index in `RawRegisters`); it used to be relative to the point's block.
- **`sunspecctl` table output** labels nested groups by name and instance, indented under their parent (`[module 1]`, `[Crv 2]` → `[Pt 1]`), instead of `[Repeating[0]]`. JSON output follows the new `DecodedModel` shape.
- **Warning texts** for registers that do not fit a group changed, and warnings from nested groups carry the group path.

## Changes

### Added

- **`sunspecctl` serial settings** — new global flags `--baud`, `--data-bits`, `--parity` (`none`, `even`, `odd`) and `--stop-bits` for `rtu://` and `ascii://` URLs. Previously only `--url` was passed on, so a serial device could be reached only at the go-modbus defaults (19200 baud, no parity, 2 stop bits). Example: `sunspecctl models --url rtu:///dev/ttyUSB0 --baud 9600 --stop-bits 1 --unit-id 3`. Invalid values are rejected before the port is opened. The flags are ignored for network URLs.
- **Modbus ASCII** — `ascii://<device>` and `asciiovertcp://host:port` URLs work with the library and the CLI, through go-modbus v1.2.
- **Weekly SunSpec models check** — a GitHub Actions workflow ([`models-check.yml`](.github/workflows/models-check.yml)) compares `models/` with [sunspec/models](https://github.com/sunspec/models) every Monday. When upstream has new, changed or removed models it opens an issue (or updates the open one) showing what changed — the points that differ and the JSON diff — and closes it once `models/` is back in sync.
- **`check-models.sh`** / **`make models-check`** — the read-only check behind that workflow. `--diff` prints the point-level and JSON differences; `--diff-file FILE` writes them to a file.
- **CONTRIBUTING.md**, **SECURITY.md**, **codecov.yml** (project target 90%).
- **`.golangci.yml`** — the lint configuration `make lint-ci` refers to (errcheck, govet, staticcheck, ineffassign, misspell, godot, nilerr, exhaustive, gofmt, goimports).
- **`make fmt-check`** — part of `make check`. Formatting is done with the `gofmt` of the Go version in `go.mod` (the one CI uses), and the check fails if that `gofmt` or the locally installed one would change a file.

### Changed

- **README** — rewritten: what the library does and for whom, supported models, decoded values, serial and other transports, CLI installation, FAQ.
- **Doc comments** — every exported identifier in `sunspec`, `registry` and `testutil` is documented.
- **Tests** — statement coverage raised from 58% to **98%** of the library and tooling packages, and from 4% to 99% for `sunspecctl`; the generator, the model parser, the fixture server and every `sunspecctl` command are now tested. A test regenerates the registry from `models/` and fails if `registry/models_gen.go` is out of date.
- **Coverage gate** — `make check` now measures every package (it was the library packages only) and requires 90% (was 75%). CI tests `cmd/sunspecctl` too.
- **Not-implemented detection for strings and addresses** — a `string` point that is all NUL, an `ipaddr` of `0.0.0.0`, an all-zero `ipv6addr` and an `eui48` of `FF:FF:FF:FF:FF:FF` are now reported with `Implemented: false`, as the SunSpec specification defines. They used to be reported as implemented, so `sunspecctl` listed empty rows for them. **Behaviour change** for code that relied on `Implemented` being true for these.

### Fixed

- **Models with nested or sibling groups were decoded incompletely.** The registry kept one fixed and one repeating block per model. The DER curve and trip models (705–710, 712) and 64410/64411 lost the groups nested inside their repeating group (the curve points), and model 704 kept only the first of its four sibling groups, treated as repeating. Every group of every model is now in the registry and decoded.
- **Group counts are taken from the model.** The number of instances of a nested group now comes from its count point (`NCrv`, `NPt`, `NStr`, …) or its fixed count, as the model defines; a group without a count occurs exactly once. Only groups defined with count 0 repeat for the remaining length of the model, as before.
- **Scale factors in nested groups.** A named scale factor is looked up in the point's own group instance first and then in the enclosing groups, so curve points scaled by a factor defined at the top of the model are scaled. A scale factor that a nested group reports as not implemented does not hide a usable one further out.
- **Scaled values had floating-point artefacts.** A value such as 95 with scale factor -2 came out as `0.9500000000000001`; it is now exactly `0.95`.
- **EUI-48 (MAC address) points were decoded from the wrong registers.** An `eui48` point is four registers with the address in the low 48 bits; the decoder read the first three registers, which shifted the address by two bytes and dropped its last two. Affects models 11 and 16.
- **JSON encoding failed for float models with unimplemented points.** A float point the device reports as not implemented is NaN, which `encoding/json` rejects, so `sunspecctl read --json` (and `read-model`, `read-point`, `poll*`) failed on models such as 111–113 and 211–214, and so did `json.Marshal` on a `DecodedModel`. `DecodedPoint` now encodes non-finite values as `null`; field names are unchanged.
- **Reads that reached the end of the address space wrapped around.** A model read split into several Modbus requests could wrap from register 65535 to 0 and return those registers as if they were contiguous. Such a range is now rejected before any I/O.
- **Panics on nil or malformed input** replaced by errors or safe behaviour: a nil `*modbus.Client` (`Detect`, `Discover`, `Open`, `ReadModel`, `ReadPoint`, `ReadAll`), `NewDevice` with a nil discovery result, `DecodeModel` with a nil schema, `registry.Register(nil)`, and caller-supplied schemas whose points have a negative offset or fewer registers than their type needs.
- **Enum symbols with negative values** no longer match an unimplemented `enum32` value.

## Dependencies

- Go 1.23+
- [otfabric/go-modbus](https://github.com/otfabric/go-modbus) `v1.1.3` → **v1.2.1** (Modbus ASCII transport, corrected function probing, server and engine fixes; see its release notes)
- [spf13/cobra](https://github.com/spf13/cobra) v1.10.2 (CLI only)

---

## v0.3.1

**Date:** 2026-07-30
**Previous release:** v0.3.0

## Summary

Patch release: bump go-modbus to v1.1.3 (pulls go-serial v0.1.6). No API or
behaviour changes.

## Changes

### Dependencies

- **go-modbus** — `v1.1.1` → **v1.1.3**
- **go-serial** (indirect) — `v0.1.5` → **v0.1.6**

### Unchanged

- No API, wire, or behavioural changes. Import path remains `github.com/otfabric/go-sunspec`.

---

## v0.3.0

**Date:** 2026-07-29

## Summary

Align documentation and error contracts with the rest of the otfabric library family. Sync SunSpec JSON models from upstream and regenerate the compiled registry. Minor release: new exported error types and removal of an unused sentinel.

## Changes

### Added

- **doc.go** — package overview covering scope, ownership, concurrency, and entry points.
- **API.md** / **ERRORS.md** — public API and error taxonomy.
- **DecodeError** — typed decode failure with model/point location fields.
- **ErrPointNotFound** — sentinel for missing point names in `ReadPoint`.
- Runnable examples for `DecodeModel` and registry lookups.
- **Makefile `vuln`** — `govulncheck ./...`, included in `make check`.
- **Makefile coverage targets** — `coverage`, `coverage-html`, `coverage-check` (minimum 75% on library packages `.` and `./registry`), and `coverage-clean`.
- **models/README.md** — documents upstream source of the JSON model definitions ([sunspec/models](https://github.com/sunspec/models) `json/` on `master`).
- Additional unit/integration coverage for `Detect`, `ReadPoint`, `toGMSunspecOptions`, IPv6 decode, scale application, and registry length helpers.

### Changed

- **README** — quickstart handles `modbus.New` / `Open` errors; documents ownership, concurrency, inherited Modbus observability; corrects `registry.All()` return type; documents model provenance and Apache-2.0 upstream license.
- **ErrUnknownModel** — comment clarifies it means “not in discovery”, not “no local schema”.
- **ErrPartialRead** — wraps the underlying Modbus cause with `%w`.
- **Makefile** — exports `GOWORK=off` so checks and builds ignore a parent `go.work` and run as a standalone module; `make check` enforces the coverage gate.
- **SunSpec models** — refreshed `models/*.json` from [sunspec/models](https://github.com/sunspec/models) `master` via `./sync-models.sh` (59 files updated; still 112 compiled models). Mostly description-text cleanup; **model 64415** gains `SubscribedResource` and `SubscriptionEna`.
- **registry/models_gen.go** — regenerated with `make generate` to match the synced JSON.

### Removed

- **ErrUnsupportedPointType** — unused. Unsupported point types continue to produce `DecodedModel` warnings with raw registers retained.

## Dependencies

- Go 1.23+
- [otfabric/go-modbus](https://github.com/otfabric/go-modbus) v1.1.1
- [spf13/cobra](https://github.com/spf13/cobra) v1.10.2 (CLI only)

---

## v0.2.3

**Date:** 2026-07-08

## Summary

Prepare the repository for public open-source release under the MIT License. Add license files and SPDX headers, and standardize README badges for publication.

## Changes

### Added

- **LICENSE** — root MIT license file (`Copyright (c) 2026 OT Fabric`).
- **SPDX headers** — `// SPDX-License-Identifier: MIT` on all first-party Go source files.
- **README** — license section and table of contents.

### Changed

- **README badges** — standardized badge block for public release: added pkg.go.dev reference, removed Go Report Card, switched Codecov to a tokenless public URL, and normalized the release badge label.
- **go-modbus** — upgraded `github.com/otfabric/go-modbus` from v1.0.4 to v1.1.1.

## Dependencies

- Go 1.23+
- [otfabric/go-modbus](https://github.com/otfabric/go-modbus) v1.1.1
- [spf13/cobra](https://github.com/spf13/cobra) v1.10.2 (CLI only)

---

## v0.2.2

**Date:** 2026-03-23

## Summary

Remove stale `github.com/otfabric/sunspec` dependency left over from the repository rename. All internal imports and documentation now use the current module path `github.com/otfabric/go-sunspec`.

## Changes

### Changed

- **Module imports** — replaced all `github.com/otfabric/sunspec` imports with `github.com/otfabric/go-sunspec` across 10 source files.
- **go.mod / go.sum** — removed the `github.com/otfabric/sunspec v0.2.0` dependency (circular self-reference under the old name).
- **README** — updated title, badges, install command, code examples, project structure, and dependency list to reflect the `go-sunspec` / `go-modbus` module paths.

## Dependencies

- Go 1.23+
- [otfabric/go-modbus](https://github.com/otfabric/go-modbus) v1.0.4
- [spf13/cobra](https://github.com/spf13/cobra) v1.10.2 (CLI only)

---

## v0.2.1

**Date:** 2026-03-23

## Summary

Enhance `sunspecctl` build metadata and improve project layout. The CLI binary now embeds full version, tag, commit, and build-date information via `-ldflags`, and local builds output to `./bin/`.

## Changes

### Changed

- **Version command** — `sunspecctl version` now prints version, git tag, commit hash, and build date (previously only printed the version string). Supports `--json` for structured output.
- **Build-time ldflags** — Makefile passes `-X main.version`, `-X main.tag`, `-X main.commit`, and `-X main.buildDate` to match the CI release workflow.
- **Build output directory** — `make build` and `make build-cli` now output the binary to `./bin/sunspecctl` instead of the project root. `make install` and `make clean` updated accordingly.

### Added

- **Release workflow** — new `release-sunspecctl-binary` job in `release.yml` publishes cross-platform `sunspecctl` binaries via the shared `go-binary-release` workflow with full ldflags.

## Dependencies

- Go 1.23+
- [otfabric/go-modbus](https://github.com/otfabric/go-modbus) v1.0.4
- [spf13/cobra](https://github.com/spf13/cobra) v1.10.2 (CLI only)

---

## v0.2.0

**Date:** 2026-03-17

## Summary

Migrate from `github.com/otfabric/modbus` to `github.com/otfabric/go-modbus`. The library now uses the shared transport and register API from go-modbus and delegates SunSpec marker detection and model-header enumeration to `go-modbus/sunspec`, while keeping this package as the high-level decoded SunSpec API.

## Changes

### Breaking

- **Modbus dependency** — `github.com/otfabric/modbus` v0.2.2 replaced by `github.com/otfabric/go-modbus` v1.0.1. Import paths and type names change:
  - Import: `github.com/otfabric/go-modbus` (and `github.com/otfabric/go-modbus/sunspec` where low-level discovery types are needed)
  - `*modbus.ModbusClient` → `*modbus.Client`
  - `modbus.ClientConfiguration` → `modbus.Config`
  - `modbus.NewClient(...)` → `modbus.New(...)`
  - `modbus.ServerConfiguration` → `modbus.ServerConfig` (test utilities)
- **Public API** — SunSpec result types are now owned by this package instead of re-exported from modbus:
  - `Detect()` now returns `*sunspec.DetectionResult` (was `*modbus.SunSpecDetectionResult`)
  - `ModelInstance.Header` is now `sunspec.ModelHeader` (was `modbus.SunSpecModelHeader`)
  - `DiscoveryResult.Raw` is now `*gmsunspec.DiscoveryResult` (was `*modbus.SunSpecDiscoveryResult`). New types `DetectionResult`, `ProbeAttempt`, and `ModelHeader` are defined in this package.

### Added

- **CLI** — `newClient()` now sets `Timeout`, `DialTimeout` (5s), and `Logger` (NopLogger) explicitly for clearer transport behaviour.

### Unchanged

- Discovery flow, registry enrichment, decode API, and CLI commands (`detect`, `models`, `read`, `read-model`, `read-point`, `poll`, etc.) are unchanged. Behaviour and output shape remain compatible.

## Dependencies

- Go 1.21+
- [otfabric/go-modbus](https://github.com/otfabric/go-modbus) v1.0.1
- [spf13/cobra](https://github.com/spf13/cobra) v1.10.2 (CLI only)

---

## v0.1.3

**Date:** 2026-03-12

## Summary

Export SunSpec protocol constants so downstream consumers (e.g. strategies parsing raw `ScanResult.Data`) can reference the canonical marker, end-model sentinel, and default base address values directly instead of maintaining mirrored copies.

## Changes

### Changed

- **Dependency upgrade** — `github.com/otfabric/modbus` v0.2.1 → v0.2.2
- **SunSpec constants** — The following values are now re-exported from the modbus library:
  - `SunSpecMarkerReg0` (`0x5375`) / `SunSpecMarkerReg1` (`0x6E53`) — "SunS" marker registers
  - `SunSpecEndModelID` (`0xFFFF`) / `SunSpecEndModelLength` (`0`) — end-of-chain sentinel
  - `SunSpecDefaultBaseAddresses` (`[]uint16{0, 40000, 50000, 1, 39999, 40001, 49999, 50001}`) — default probe addresses
- **Test utilities** — `testutil.NewSunSpecFixture` now uses the modbus constants instead of hardcoded magic numbers

### Unchanged

- All SunSpec discovery methods, types, and behaviour unchanged. This is a purely additive API change.

## Dependencies

- Go 1.21+
- [otfabric/modbus](https://github.com/otfabric/modbus) v0.2.2
- [spf13/cobra](https://github.com/spf13/cobra) v1.10.2 (CLI only)

---

## v0.1.2

**Date:** 2026-03-12

## Summary

Adds polling commands, version command, build-time version injection, linter fixes, and switches to the published modbus dependency.

## New Commands

- **`poll`** — repeatedly read and decode all models at a configurable interval
- **`poll-model`** — repeatedly read a specific model by ID
- **`poll-point`** — repeatedly read a single named point
- **`version`** — print the build version

All poll commands support `--interval` (default `30s`, accepts `s`/`m`/`h`) and `--count` (default `0` = infinite, Ctrl-C to stop).

## Changes

- **Published dependency** — replaced local `replace` directive with `github.com/otfabric/modbus v0.2.1`
- **Version injection** — `sunspecctl version` prints the version set via `-ldflags` at build time (defaults to `git describe`)
- **Linter fixes** — resolved all 13 `errcheck` findings in CLI and test utilities; `make lint` now runs `golangci-lint` (matching CI)
- **CI cleanup** — removed unnecessary modbus checkout step from CI and release workflows

## Testing

- 6 new tests for poll loop logic (single, multiple, error stop, context cancellation, infinite loop, per-iteration timeout)
- All existing tests continue to pass (21 decode + 7 integration + 5 registry)

## Dependencies

- Go 1.21+
- [otfabric/modbus](https://github.com/otfabric/modbus) v0.2.1
- [spf13/cobra](https://github.com/spf13/cobra) v1.10.2 (CLI only)

---

## v0.1.0

**Date:** 2026-03-12

## Summary

Initial release of the `otfabric/sunspec` Go library and `sunspecctl` CLI tool for reading SunSpec devices over Modbus.

## Highlights

- **Auto-discovery** — detect SunSpec presence and enumerate all models on a device
- **Full decoding** — all standard SunSpec point types: int, uint, float, string, enum, bitfield, IP addresses, accumulators, scale factors
- **Scale factor resolution** — automatic SF resolution for both point-reference and literal scale factors
- **Repeating blocks** — full support for meters, MPPTs, and other models with repeating groups
- **Registry** — compiled metadata for 112 SunSpec models, generated from upstream JSON definitions
- **Batch reading** — transparent splitting of reads >125 registers
- **`sunspecctl` CLI** — `detect`, `models`, `read`, `read-model`, `read-point` commands with `--json` and `--raw` output

## What's Included

### Library (`github.com/otfabric/go-sunspec`)
- `Detect()` / `Discover()` / `Open()` — device discovery
- `Device.ReadAll()` / `ReadModel()` / `ReadModelByID()` / `ReadPoint()` — read API
- `DecodeModel()` — standalone model decoder
- `registry` package — model metadata lookups (`ByID`, `Known`, `All`, `Count`)

### CLI (`cmd/sunspecctl`)
- Cross-platform binaries: linux/amd64, linux/arm64, linux/armv7, darwin/amd64, darwin/arm64
- Shell completion: bash, zsh, fish, powershell

### Testing
- 21 unit tests covering all point type decoders and scale factor resolution
- 7 integration tests with in-process Modbus server fixtures
- 5 registry tests

## Dependencies

- Go 1.21+
- [otfabric/modbus](https://github.com/otfabric/modbus) v0.2.0+
- [spf13/cobra](https://github.com/spf13/cobra) v1.10.2 (CLI only)

---
