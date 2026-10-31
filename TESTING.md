# Testing go-sunspec

How to run each tier. No hardware is needed. What each layer is for, and how to add to it, is in [CONTRIBUTING.md](CONTRIBUTING.md#test-layers).

## Tiers

| Tier | Command | Prerequisites | In CI |
|---|---|---|---|
| Unit, round trip | `make test` | none | yes, Go 1.23 to 1.26, with `-race` |
| End-to-end | `make e2e` (`TestE2E*`, `TestIntegration*`) | none | yes, as part of the package tests |
| Chaos, stress | part of `make test` | none | yes, for a second or two each |
| Soak | `make soak` | none | no |
| Fuzz | `make fuzz` (`FUZZTIME`, default 30s) | none | yes, 10s per target |
| Benchmarks | `make bench` (`BENCHTIME`) | none | no |
| Cross-compile | `make cross` | none | no |
| Interop | `make interop` (build tag `interop`) | Docker | yes, Interop workflow and daily |
| Model drift | `make models-check` | network | weekly workflow, reports only |

`make interop` runs [`interop/`](interop/) against pysunspec2 in Docker ([INTEROPERABILITY.md](INTEROPERABILITY.md)). With `SUNSPEC_REQUIRE_INTEROP=1`, which `make interop` and the workflow set, missing Docker is a failure and not a skip. `SUNSPEC_INTEROP_PYSUNSPEC2_IMAGE` replaces the pinned image.

`make coverage-pkg-check` fails when one package is below `COVER_PKG_MIN` (90); CI runs it in the `coverage-packages` job.

The end-to-end tier reads the library's own `Server`, behind a real go-modbus TCP server on loopback, through the public client path. `e2e_test.go` starts with the feature-to-test matrix.

## Environment variables

| Variable | Effect |
|---|---|
| `SUNSPEC_CHAOS_SEED` | Replays one seed (a failing run logs it): `SUNSPEC_CHAOS_SEED=<seed> go test -race -run <Test> .` |
| `SUNSPEC_CHAOS_DURATION`, `SUNSPEC_STRESS_DURATION` | Soak mode of the chaos and stress tests (`make soak` sets them) |

## Coverage

`make coverage` writes `coverage.out` and `coverage.txt` over `./...`. `make coverage-check` fails below `COVERAGE_MIN` (90), the same minimum as `coverage-min` in `.github/workflows/ci.yml`. No package is excluded.

## Fuzz regressions

`make fuzz` discovers the targets: `FuzzDecodeModel` and `devicefile.FuzzParse`. A failing input is written to `testdata/fuzz/<target>/` of the package. Commit it with the fix: `go test` replays it from then on.
