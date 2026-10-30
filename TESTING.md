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
| Model drift | `make models-check` | network | weekly workflow, reports only |

There is no interop tier: no independent implementation is in the test suite ([INTEROPERABILITY.md](INTEROPERABILITY.md)).

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
