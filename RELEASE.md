# go-sunspec Releases

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
- [otfabric/sunspec](https://github.com/otfabric/sunspec) v0.2.0
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

### Library (`github.com/otfabric/sunspec`)
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
