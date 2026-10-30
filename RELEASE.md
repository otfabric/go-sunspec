# go-sunspec Releases

## v0.8.0

**Date:** 2026-10-30
**Previous release:** v0.7.1

### Dependencies

- `go-modbus` v1.9.0 → v1.10.0.

---

## v0.7.1

**Date:** 2026-10-30
**Previous release:** v0.7.0

## Summary

Dependency update: go-modbus v1.9.0. A read from a device that never answers
now ends at the client's `Config.Timeout` even when the context has a later
deadline. No API changes.

## Changes

### Dependencies

- **go-modbus v1.8.0 → v1.9.0.** A request ends at `Config.Timeout` or its
  context's deadline, whichever comes first; a context deadline used to
  replace the timeout.
  - `ReadModels`, `ReadAll`, `ReadModel`, `ReadPoint`, `Detect` and
    `Discover` no longer let one device that never answers hold a request
    for the caller's whole deadline.
  - A caller that relied on a long context deadline to let a slow device
    answer after `Timeout` must raise `Timeout` instead.

### Tests

- `TestReadModelsTimeoutStopsBatch` runs under a context with a deadline;
  the client timeout ends the request that gets no answer.

---

## v0.7.0

**Date:** 2026-10-30
**Previous release:** v0.6.1

## Summary

`Device.ReadModels` reads several models with fewer requests: adjacent models
share a read request, and each model gets its own result. `Device.ModelsByID`
finds every instance of a model. Requires go-modbus v1.8.0. `ReadAll` and the
other read methods are unchanged.

## Changes

### Added

- **`Device.ReadModels(ctx, insts) ([]ModelResult, error)`**:
  - **Requests:** plans the instances with the go-modbus `batch` package.
    Adjacent models share a request of up to 125 registers, and no register
    outside them is read.
  - **Header check:** every request carries the headers of its models,
    checked as `ReadModel` checks them (`ErrModelChanged`).
  - **Splitting:** a model of up to 125 registers including its header
    (`Length` ≤ 123) is never split, so its values and scale factors come
    from one response. A longer model gets requests of its own and keeps
    `ErrPartialRead`.
  - **Results:** one `ModelResult` (`Instance`, `Model`, `Err`) per instance,
    in input order, each as `ReadModel` returns it. The error is nil only when
    every instance succeeded; otherwise it wraps the first failure.
  - **Exceptions:** when a Modbus exception fails a request shared by several
    models, as from a device that refuses reads across model boundaries, each
    of them is read again on its own.
  - **Stopping:** a timeout, transport error or ended context stops the batch.
    Instances not read have a nil `Model` and an `Err` wrapping
    `batch.ErrNotAttempted` and the cause.
  - **Opt in** for the whole chain: `d.ReadModels(ctx, d.Discovery.Models)`.
- **`Device.ModelsByID(id) []ModelInstance`**: every instance of a model ID,
  in chain order (for example one model 160 per MPPT tracker set), as copies
  that do not change `Device.Discovery`.

### Performance

On an inverter chain (models 1, 103, 120–123):

| Delay per request | `ReadAll` | `ReadModels` |
|---|---|---|
| 0 | 6 requests, 238 µs | 3 requests, 132 µs |
| 5 ms | 35 ms | 17.7 ms |
| 20 ms | 128 ms | 64 ms |

### Dependencies

- **go-modbus v1.7.2 → v1.8.0**, which adds the `batch` package.

### Tests and tooling

- `make bench`: end-to-end benchmarks of `ReadAll`, `ReadModels`, `ReadModel`
  and `ReadPoint` at 0, 5 and 20 ms per request, reporting requests and PDU
  bytes, and CPU benchmarks of `DecodeModel`.

---

## v0.6.1

**Date:** 2026-10-29
**Previous release:** v0.6.0

## Summary

Dependency update: go-modbus v1.7.2 with go-serial v0.1.8. No API changes.

## Changes

### Dependencies

- **go-modbus v1.5.0 → v1.7.2.** Picks up the go-modbus robustness and
  protocol fixes of v1.5.1–v1.7.2; see go-modbus's release notes.
- **go-serial v0.1.6 → v0.1.8**, which fixes two POSIX serial I/O bugs:
  - `Write` no longer loses data when the tty output buffer is full. It
    used to return a short count with a nil error (or `n == -1` with
    `EAGAIN`); it now blocks until every byte is written.
  - `Read` works when the port's file descriptor is 1024 or higher. It
    used to return a spurious `io.EOF` at once.

### Documentation

- API.md names the go-modbus `RetryPolicy` field in the observability
  example; security contact is security@otfabric.io.

---

## v0.6.0

**Date:** 2026-10-14
**Previous release:** v0.5.1

## Summary

The device file, the JSON description of a SunSpec device that `sunspecctl serve` runs and `sunspecctl capture` writes, is now part of the library: the new package `devicefile` parses, validates, serves, captures and writes it, and publishes a JSON Schema of the format. The module moves to go-modbus v1.5.0, whose clients can be restricted to a list of function codes; `ReadFunctionCodes` gives the list for a client that can read a SunSpec device and cannot write to it. Reading, decoding, encoding and serving are unchanged.

## Changes

### Added

- **Package `devicefile`** (`github.com/otfabric/go-sunspec/devicefile`) — the device file format, moved from sunspecctl; sunspecctl v0.2.0 and sunspec-explorer v0.0.3 use it. `Parse` and `Load` read a file, keeping the order of its points; unknown members, a point given twice and data after the document are errors, with line and column for malformed JSON. `File.Server` builds the `Server` the file describes (`ServerOptions` replaces the unit ID and base address and takes an `OnWrite` hook), `File.ServerModels` gives the models for your own `NewServer` call, `Group.Values` the values of one group instance. `Capture` reads every model of a `Device` and `FromDecoded` describes one decoded model: by point values when the model decoded cleanly and reads back the same, by registers otherwise or on request. `Template` is a model with every point `null`. `File.Marshal`, `Write` and `WriteFile` write the indented form. The files written, the files accepted and the error messages are those of sunspecctl v0.1.x.
- **JSON Schema of the device file** — `devicefile/devicefile.schema.json` (draft 2020-12, `$id` `https://otfabric.io/schemas/sunspec/devicefile-1.schema.json`), embedded and returned by `devicefile.Schema()`. It describes the form of a file; the tests validate captured files, the template of every registry model and the documented examples against it.
- **`ReadFunctionCodes()`** — returns the two Modbus function codes the client side of this package sends, Read Holding Registers (0x03) and Read Input Registers (0x04), as a new slice for `modbus.Config.AllowedFunctionCodes`. A client created with it refuses every other function code before anything is sent; `Detect`, `Discover`, `Open` and all `Device` methods work through it unchanged, which `TestReadOnlyClient` verifies for holding and input registers.
- **Errors** — `devicefile.ErrSyntax` with the typed `*devicefile.SyntaxError` (line, column) and `devicefile.ErrInvalid` with `*devicefile.ModelError` (model position and ID). Their messages are the ones sunspecctl printed.

### Changed

- **go-modbus** — requires `github.com/otfabric/go-modbus` v1.5.0 (v0.5.1 required v1.4.2). It adds `modbus.Config.AllowedFunctionCodes`: a client refuses function codes outside the list with an error wrapping `modbus.ErrFunctionCodeNotAllowed` before a connection is dialled or a byte is sent. Without the option a client behaves as before.
- **Test dependency** — the tests of `devicefile` use `github.com/santhosh-tekuri/jsonschema/v6` to validate files against the schema. No package of the library imports it: programs that use go-sunspec do not build it, and the library itself still depends on go-modbus alone.
- **Documentation** — README, API.md, ERRORS.md and SECURITY.md describe device files and the read-only client.

### Unchanged

- No changes to reading, decoding, encoding, serving, the model definitions or existing signatures. `Discover` and `Device` take any `*modbus.Client`, restricted or not.

---

## v0.5.1

**Date:** 2026-10-13
**Previous release:** v0.5.0

## Summary

A served device can stay silent: `ServerOptions.SilentToOtherUnits` makes a `Server` ignore requests for another unit ID instead of answering with an exception, and a broadcast (unit ID 0) is no longer answered at all. The module moves to go-modbus v1.4.2, which makes one discovery error uniform (see Changed). Reading is otherwise unchanged.

## Changes

### Added

- **`ServerOptions.SilentToOtherUnits`** — when set, a `Server` does not answer requests addressed to another unit ID: the client runs into its timeout and the connection stays open, as with a device on a serial line. The default is unchanged: such a request is answered with exception 0x0B (gateway target device failed to respond), which tells a client at once that the unit is not there.

### Changed

- **Broadcast requests are not answered** — a request for unit ID 0, the Modbus broadcast address, gets no response from a `Server` and is not carried out. v0.5.0 answered it with exception 0x0B like any other foreign unit ID.
- **go-modbus** — requires `github.com/otfabric/go-modbus` v1.4.2 (v0.5.0 required v1.4.0, though its documents still said v1.3.0). `ServerOptions.SilentToOtherUnits` and the broadcast handling use `modbus.ErrNoResponse`, added in v1.4.0.
- **`ErrModelChainInvalid` for every model that does not fit** — a device whose model runs beyond address 65535, or ends on register 65534 so that no header can follow it, now fails `Discover` with `ErrModelChainInvalid`. With go-modbus v1.4.0 these two cases surfaced as `modbus.ErrProtocolError` and as a parameter error of the failing read; only a model ending exactly on register 65535 gave `ErrModelChainInvalid`. Discovery failed in all three cases and still does (fixed in go-modbus v1.4.1).
- **Documentation** — README, API.md and ERRORS.md describe the answers to foreign unit IDs and broadcasts.

### Unchanged

- No changes to reading, decoding, encoding, the model definitions or existing signatures. A `Server` created without the new option behaves as in v0.5.0, except for broadcasts.

---

## v0.5.0

**Date:** 2026-10-13
**Previous release:** v0.4.2

## Summary

go-sunspec can now **serve** SunSpec devices, not only read them: build a device from models and point values and serve it over Modbus, as a simulator or as the SunSpec interface of your own equipment. The `sunspecctl` command-line tool moves to its own repository, [otfabric/sunspecctl](https://github.com/otfabric/sunspecctl); go-sunspec is now the library only. The existing API is unchanged in its signatures; reading a model now also verifies that the device still has it (see Changed). The test suite gained end-to-end, chaos and parallel stress layers, which found the defects listed under Fixed.

## Changes

### Added

- **`EncodeModel(meta, values)`** — the inverse of `DecodeModel`: builds a model's registers from the registry schema and values given by point name, without I/O. Covers every point type the decoder handles, enum and bitfield values by symbol name, raw registers for any point, and nested and repeating groups with their count points. Points that are not set are encoded as the type's "not implemented" value.
- **`Values`** — the values of a group instance (`Points`, `Groups`), with `Set`, `Add` and `Clone`. Points with a scale factor take the engineering value: it is divided by 10^sf and rounded to the nearest integer, halves away from zero. A value that does not fit its type after scaling is an error, never wrapped or clamped.
- **`DecodedModel.Values()`** — turns a decoded model back into `Values`, so that a device that was read can be rebuilt.
- **`Server`** (`NewServer`, `ServerOptions`, `ServerModel`) — a SunSpec device in memory: marker, model headers, data and end marker at a base address, for one unit ID, in holding or input registers. It is a go-modbus `RequestHandler` to pass to your own `modbus.NewServer`; `Server.Serve(ctx, conf)` runs one for the duration of a context. Reads may span models; unmapped addresses answer exception 2. Vendor and unknown models are served from raw registers.
- **Live updates** — `Server.SetPoint`, `SetValues`, `Update` and `SetRegisters` change values while the device is being read, atomically per model; `Server.Model` returns a decoded snapshot.
- **Client writes** — a `Server` accepts writes (FC 6, FC 16) to points whose schema access is `RW` and refuses everything else with exception 2. `ServerOptions.OnWrite` receives each write decoded (`ServerWrite`) before it takes effect and can refuse it.
- **Errors** — `ErrEncode` with the typed `*EncodeError` (model, group path, point, type), `ErrValueOutOfRange`, `ErrModelLayout`, `ErrModelChanged`.
- **End-to-end tests** (`e2e_test.go`) — the library's `Server` behind a real go-modbus TCP server, read through `Detect`, `Discover`, `ReadAll`, `ReadModel`, `ReadModelByID` and `ReadPoint`: every registry model with generated values, every `DiscoverOptions` field, scale-factor and nested-group cases, vendor models, request splitting, live updates, client writes and every documented error. The file starts with a feature-to-test matrix.
- **Round-trip tests** — `EncodeModel` → `DecodeModel` for every model of the registry with generated values for every point type, including repeating groups.
- **Chaos tests** (`chaos_test.go`, `chaos_proxy_test.go`) — a fault-injecting TCP proxy between client and served device (dropped, reset, truncated, stalled, split, duplicated, delayed and corrupted frames) and misbehaving devices (exceptions, a vanishing marker, a changing or endless chain, lying lengths, garbage registers). Asserted: no panic, no hang, no goroutine leak, and never a wrong value returned as valid.
- **Parallel stress tests** (`stress_test.go`) — concurrent discovery and reads through one client, a pooled client and many clients while values are updated and clients write, and across server stop/start cycles; a read must never be torn or go back in time.
- **`make soak`** — runs the chaos and stress tests for `SOAKTIME` each (default 2m); `SUNSPEC_CHAOS_SEED` replays a logged seed.

### Changed

- **`Device.ReadModel`, `ReadModelByID`, `ReadPoint`, `ReadAll` verify the model header** — a model's ID and length registers are now read together with its data and compared with the discovery result. A device whose model chain changed after `Discover` (firmware update, reconfiguration, another device behind the same address) is reported with the new `ErrModelChanged` instead of having its registers decoded with a stale layout. On the wire a model read now starts two registers earlier: a model of 124 or 125 data registers takes two requests instead of one, the chunks of longer models shift by two registers, and the position in an `ErrPartialRead` message is relative to the header address. A `Device` built by hand with `NewDevice` must describe models whose headers are on the device.
- **Discovery warns about an unterminated chain** — a chain cut at `DiscoverOptions.MaxModels` (256 by default) without reaching the end marker adds "model chain not terminated" to `Device.Discovery.Warnings`. It was cut silently before.
- **Package documentation** — `doc.go`, README, API.md, ERRORS.md and SECURITY.md describe serving.
- **Release workflow** — publishes the library release only; the binary job is removed.
- **README, CONTRIBUTING, SECURITY** — the CLI documentation and install instructions moved to the sunspecctl repository.

### Fixed

- **Stale layout decoded as valid data** — after a device changed its model chain, reads through an existing `Device` returned the registers at the old addresses decoded with the old schemas, without error. Found by the chaos tests (`TestChaosChainChanges`, `TestChaosLyingLengths`); fixed by the header verification above.
- **Endless model chain looked complete** — a device whose chain never reaches an end marker was discovered as a device with exactly `MaxModels` models and no indication that the walk had been cut. Found by `TestChaosEndlessChain`; fixed by the warning above.

### Removed

- **`cmd/sunspecctl`** — the command is no longer part of this module. `go install github.com/otfabric/go-sunspec/cmd/sunspecctl@latest` stops at v0.4.2; use `go install github.com/otfabric/sunspecctl@latest`. The releases of the new repository are numbered from v0.1.0.
- **`sunspecctl` release binaries** — releases of this repository no longer carry `sunspecctl-<version>-*` archives. Download them from the [sunspecctl releases](https://github.com/otfabric/sunspecctl/releases). The archives attached to v0.4.2 and earlier stay where they are.
- **`github.com/spf13/cobra` dependency** — it was only used by the command. The module now depends on go-modbus alone.
- **Makefile** — the `build-cli`, `build-all`, `release-all` and `install` targets are gone; `make build` regenerates the registry and builds the packages.

### Unchanged

- No existing function or type changed its signature, and the model definitions are those of v0.4.2. Import path remains `github.com/otfabric/go-sunspec`. `testutil` remains public and source compatible: sunspecctl's tests use it.

---

## v0.4.2

**Date:** 2026-10-11
**Previous release:** v0.4.1

## Summary

Patch release: adopts go-modbus v1.3.0. No API or model changes.

## Changes

### Changed

- **go-modbus** — requires `github.com/otfabric/go-modbus` v1.3.0 (was v1.2.1). What callers of `Client` and users of `sunspecctl` notice on TCP: after a request times out the connection is replaced, so a late response is never taken for the answer to the next request; an open client reconnects by itself after a dropped connection (the request that hits the dead connection still fails); and cancelling the context of a call interrupts the request in flight.

### Unchanged

- No API, wire-format or model changes. Import path remains `github.com/otfabric/go-sunspec`.

---

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
