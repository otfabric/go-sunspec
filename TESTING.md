# Testing go-sunspec

How to run each tier. No hardware is needed. What each layer is for, and how to add to it, is in [CONTRIBUTING.md](CONTRIBUTING.md#test-layers).

## Tiers

| Tier | Command | Prerequisites | In CI |
|---|---|---|---|
| Unit, round trip | `make test` | none | yes, Go 1.26 and 1.27, with `-race` |
| End-to-end | `make e2e` (`TestE2E*`, `TestIntegration*`) | none | yes, as part of the package tests |
| Chaos, stress | part of `make test` | none | yes, for a second or two each |
| Soak | `make soak` | none | no |
| Fuzz | `make fuzz` (`FUZZTIME`, default 30s) | none | yes, 10s per target; 5 minutes per target in the nightly workflow |
| 32-bit | `GOARCH=386 go test ./...` | a host that runs 386 binaries (linux/amd64) | nightly workflow |
| Benchmarks | `make bench` (`BENCHTIME`) | none | no |
| Cross-compile | `make cross` | none | no |
| Interop | `make interop` (build tag `interop`) | Docker | yes, Interop workflow and daily |
| Model checksums | `make models-verify` | none | yes, with `make generate-check` |
| Model drift | `make models-check` | network | weekly workflow, reports only |

`make interop` runs [`interop/`](interop/) against pysunspec2 and cB-Sigma in Docker ([INTEROPERABILITY.md](INTEROPERABILITY.md)). With `SUNSPEC_REQUIRE_INTEROP=1`, which `make interop` and the workflow set, missing Docker is a failure and not a skip. `SUNSPEC_INTEROP_PYSUNSPEC2_IMAGE` and `SUNSPEC_INTEROP_CBSIGMA_IMAGE` replace the pinned images. `SUNSPEC_INTEROP_OTFABRIC_IMAGE` adds the adapter `otfabric`, a released go-sunspec (the manual Self-interop workflow; never a default and never independent evidence), and `SUNSPEC_INTEROP_ADAPTERS` restricts a run to a comma-separated subset (`pysunspec2`, `cbsigma`, `otfabric`).

`make coverage-pkg-check` fails when one package is below `COVER_PKG_MIN` (90); CI runs it in the `coverage-packages` job.

The end-to-end tier reads the library's own `Server`, behind a real go-modbus TCP server on loopback, through the public client path. `e2e_test.go` starts with the feature-to-test matrix.

## Lifecycle contract matrix

The rows L1 to L16 of the organisation's lifecycle standard, for the long-lived types of this module. There are three: `Server` (state and a go-modbus `RequestHandler`: it starts no goroutine and has no `Close`), `Server.Serve` (owns a go-modbus server for the life of its context) and `Device` (borrows the caller's `*modbus.Client` and starts nothing). Connections, readers, request slots, reconnection and TLS belong to go-modbus, which has its own matrix. A row is MUST with the tests that cover it, or n/a with the reason.

| Row | Applies | Test, or why it does not apply |
|---|---|---|
| L1 Close twice | n/a | No type has `Close`. `Serve` ends once, with its context |
| L2 Concurrent Close | n/a | As L1 |
| L3 Close during a blocked read | n/a | Connections and their readers are go-modbus's. The nearest case, `Serve` ending while clients read, is in `TestStressServeCycles` (it returns; leak check) |
| L4 Close during a write | n/a | Nothing here writes a shared stream |
| L5 Close during handshake | n/a | No handshake here: TLS is go-modbus's |
| L6 Cancel a pending request | MUST | `TestReadModelsCancelled`, `TestReadModelsTimeoutStopsBatch`, `TestReadRegistersPartialRead`: the call returns the context's or the transport's error and what it had read |
| L7 Late response | n/a | Matching responses to requests is go-modbus's. That a late or repeated frame never becomes a wrong value is an invariant of the chaos tests (`TestChaosScriptedFaults`, `TestChaosWritePoint`) |
| L8 Response after reconnect | n/a | Nothing here reconnects |
| L9 Callback calls a public method | MUST | `TestL09OnWriteCallsServerMethods` (`OnWrite` calls `Models` and `Model`), `TestL09OnWriteChangesItsModel` (it updates the model being written: the write is refused with exception 4; another model: accepted), `TestL09UpdateCallsServerMethods`, `TestUpdateRunsAgainAfterAChange` |
| L10 Callback calls Close | MUST, as its nearest case | No `Close`: `TestL10OnWriteEndsServe`, an `OnWrite` that cancels the context of `Serve` (`Serve` returns nil, the write took effect, leak check) |
| L11 Slow callback | MUST | `TestL11SlowOnWrite` (while `OnWrite` blocks, other clients read every model and write another one), `TestL11SlowUpdate` |
| L12 Queue at capacity | n/a | No queues |
| L13 Concurrent sessions | MUST | `TestStressManyClients`, `TestStressServerRestart`: concurrent readers, a writer and updaters on one device, which is the design; each model stays consistent |
| L14 Failure during construction or startup | MUST | `TestNewServerErrors` (`NewServer` acquires nothing), `TestServerServe` (address in use, invalid URL: the error, and a leak check) |
| L15 Shutdown(ctx) | MUST | `TestServerServe` (returns nil, the listener is gone), `TestStressServeCycles`, `TestL15ServeShutdownCutShort` (a request that does not end within the limit: `context.DeadlineExceeded`, `Serve` does not wait, leak check once the callback returns) |
| L16 Close racing reconnect | n/a | Nothing here reconnects |

Also part of the callback contract ([API.md](API.md#the-onwrite-contract)): `TestOnWriteThatDoesNotReturnNormally` (behind a go-modbus server: exception 4, nothing changes, the `Server` keeps serving), `TestOnWriteOwnsServerWrite` (what `OnWrite` changes in its `ServerWrite` is not stored) and `TestUpdateValuesKeptAsDocumented`. These tests wait on channels; their one timeout only bounds a wait that would otherwise hang.

## Budgets and boundaries

| What | Test |
|---|---|
| Memory `DecodeModel` and `Values` take per register, every registry model, the heaviest ones at the longest length a device can announce, also with register patterns chosen per point type | `TestDecodeModelAllocationBudget`, `TestDecodeModelAllocationBudgetByType` |
| A count that announces more instances than any model holds costs what the registers hold | `TestDecodeCountAllocationBudget`, `TestDecodeCountBeyondAnInt32`, `TestCountValueBounds` |
| Memory `devicefile.Parse` and `File.Server` take for a file of `MaxFileSize` | `TestFileAllocationBudget` (package `devicefile`): empty group instances, group instances and models with an empty `"points"`, and point-value lists of 65535 numbers, strings and objects. Under the race detector the files are an eighth of that size, since parsing it takes ten times as long there; the budget is per byte |
| Point-value lists and objects of a device file (65535 entries read, 65536 refused, at any depth) | `TestPointValueListLimit` (package `devicefile`) |
| Group instances and nesting of a device file | `TestServerModelsInstanceLimit` (65534, 65535, 65536, 65537 instances), `TestServerModelsGroupsFollowTheSchema` |
| Schema lengths, offsets and sizes up to `math.MaxInt` and across `math.MaxInt32` | `TestSchemaPointBounds`, `TestSchemaGroupLengthBounds` |
| The scale factor a write needs: -11, -10, 10, 11, not implemented | `TestWritePointScaleFactor` |

The figures behind the budgets are in [LIMITATIONS.md](LIMITATIONS.md#memory). The budgets are for 64-bit platforms and hold with the race detector, which adds about a third.

Goroutine leaks are found by comparing the goroutines with those that existed before the test, one by one (`leakCheck` in `chaos_test.go`), not by counting them.

## Environment variables

| Variable | Effect |
|---|---|
| `SUNSPEC_CHAOS_SEED` | Replays one seed (a failing run logs it): `SUNSPEC_CHAOS_SEED=<seed> go test -race -run <Test> .` |
| `SUNSPEC_CHAOS_DURATION`, `SUNSPEC_STRESS_DURATION` | Soak mode of the chaos and stress tests (`make soak` sets them) |

## Coverage

`make coverage` writes `coverage.out` and `coverage.txt` over `./...`. `make coverage-check` fails below `COVERAGE_MIN` (90), the same minimum as `coverage-min` in `.github/workflows/ci.yml`. No package is excluded.

## Fuzz regressions

`make fuzz` discovers the targets: `FuzzDecodeModel`, `FuzzDiscover` (discovery and reads of arbitrary registers behind the marker, over loopback TCP), `FuzzServerRequest` (arbitrary holding register requests to a `Server`), `FuzzWritePoint` (`WritePoint` of arbitrary names and values over loopback TCP: documented errors only, a refused write changes nothing, an accepted one only the point's registers) and `FuzzParse` and `FuzzPointsUnmarshal` (`Points.UnmarshalJSON` against `encoding/json` with `UseNumber`) in package `devicefile`. A failing input is written to `testdata/fuzz/<target>/` of the package. Commit it with the fix: `go test` replays it from then on.

## When CI runs

| Event | Workflow | What runs |
|---|---|---|
| Pull request | `pr.yml` | `docs.yml` (docsync, otfabric-policy) always; `ci.yml`, `interop.yml` unless every changed file is documentation; then `OT Fabric Required Checks` |
| Push to a branch | none | nothing; dispatch `ci.yml`, `interop.yml` to test a branch without a pull request |
| Push to `main` | `docs.yml` | the light checks; the merged commit was tested as the pull request's merge commit |
| Schedule | `interop.yml`, `models-check.yml`, `nightly.yml` | the scheduled runs, on `main`: interop daily, model drift weekly, and every night each fuzz target for 5 minutes (a failing input is uploaded as an artifact) and the tests as 386 binaries |
| Release | `release.yml` | the release workflow tests the release commit again |

Documentation means an added or modified `*.md` file outside `.github/`, `testdata/` and `vendor/`, other than `AGENTS.md` and `CLAUDE.md`, and other than `API.md`, which code reads. A deleted or renamed document, or any other file, runs everything. `OT Fabric Required Checks` fails unless every job that applies succeeded; its summary lists them.
