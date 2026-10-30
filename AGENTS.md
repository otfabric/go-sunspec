# AGENTS.md — go-sunspec

## What this is

Pure Go library that reads SunSpec devices over Modbus (detect, discover, decode) and serves them (encode, `Server`, device files), on top of go-modbus. Profile: **protocol-library**. Current release: see RELEASE.md. It is implemented and tested against its own `Server` and fixture only: no independent implementation is in its test suite, and nothing is certified (LIMITATIONS.md). The CLI is the separate repository `otfabric/sunspecctl`.

## Organisation standards

This repository follows **otfabric/engineering policy 1.0**, profile **protocol-library** (`.otfabric/policy.yaml`, mode `baseline`). Where this file and the organisation standards disagree, this file wins here — say so in your report.

## Where the truth lives

| Question | Source |
|---|---|
| Model definitions | `models/` (copied from sunspec/models, see models/README.md) |
| Public API contract | API.md |
| Errors | ERRORS.md |
| What is not done or not verified | LIMITATIONS.md |
| Test layers and how to replay seeds | CONTRIBUTING.md |
| Release history | RELEASE.md (`## vX.Y.Z` … `---`) |

## Hard rules

1. Never lower the coverage minimum (90, `COVERAGE_MIN` in the Makefile and `coverage-min` in CI), add lint suppressions, or skip/delete tests to make CI pass. Never weaken a chaos, stress or fuzz invariant to get a green run: find out whether the library, go-modbus or the test is wrong.
2. Never edit `models/` or `registry/models_gen.go` by hand. Change the generator in `internal/gen`, or run `make sync`.
3. The client side stays read-only: `Detect`, `Discover` and `Device` send function codes 03 and 04 only (`ReadFunctionCodes`).
4. Code must build and pass on Go 1.23 (go.mod). No newer language or standard-library features.
5. Exported API changes break sunspecctl: this library stays on v0 or v1, and a breaking change is called out in RELEASE.md.
6. No new runtime dependency without an ADR. `santhosh-tekuri/jsonschema` is a test dependency only.
7. Never invent SunSpec semantics or device-compatibility claims; cite the model definition or record the evidence.

## Commands

| When you change… | Run |
|---|---|
| anything | `make check` |
| a decoder, encoder or device-file parsing | `make fuzz` |
| `models/` or `internal/gen` | `make generate generate-check` |
| chaos or stress tests, or the read path | `make soak`, and `go test -race -count=20 -run 'Chaos|Stress' .` |
| what upstream publishes | `make models-check` (read-only) |

## Definition of Done

`make check` green; tests for new behaviour incl. error paths; fuzz target for new decoders of untrusted input; API.md, ERRORS.md, LIMITATIONS.md and README.md updated where they describe the change; RELEASE.md section for user-visible changes.

## Git and releases

Branches and PRs. Do not push to `main`, tag or run the Release workflow unless the maintainer asks in the session.
