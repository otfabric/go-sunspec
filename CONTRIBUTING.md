# Contributing to go-sunspec

Thank you for your interest in contributing to `go-sunspec`! This document covers the guidelines for contributing.

## Getting Started

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```sh
   git clone git@github.com:<your-username>/go-sunspec.git
   cd go-sunspec
   ```
3. Create a feature branch:
   ```sh
   git checkout -b feature/my-change
   ```

## Development Setup

### Prerequisites

- Go 1.23+ (see [go.mod](go.mod)); the code must build and pass on Go 1.23, so do not use newer language or standard-library features
- [staticcheck](https://staticcheck.dev/), [golangci-lint](https://golangci-lint.run/) and [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) for the checks
- `curl`, `jq` and `git` for the model scripts
- No hardware: tests use the SunSpec fixture server in [`testutil`](testutil/)

### Build & Test

```sh
# Run all checks (format, vet, lint, vulnerability scan, race tests, coverage gate)
make check

# Run tests with the race detector
make test

# Coverage report for the library packages
make coverage

# Build the library and the sunspecctl CLI (./bin/sunspecctl)
make build
```

Run `make` (or `make help`) for the full list of targets.

## Making Changes

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Start every Go file with `// SPDX-License-Identifier: MIT`
- Keep functions focused and small
- Every exported identifier has a doc comment that tells a caller something useful
- Comments end with a period (enforced by the linter)

### Formatting

`make fmt` formats with the `gofmt` of the Go version in `go.mod`, which is the version CI uses, not necessarily the one you have installed. `make fmt-check` (part of `make check`) fails if either that `gofmt` or your local one would change a file: `gofmt` output differs slightly between Go releases, and a file the two disagree on fails CI. If they disagree, restructure the code (for example, move trailing comments onto their own lines) rather than picking a side.

### SunSpec models and generated code

- The JSON files in [`models/`](models/) are copied as-is from [sunspec/models](https://github.com/sunspec/models). **Do not edit them by hand**; a wrong definition must be fixed upstream
- [`registry/models_gen.go`](registry/models_gen.go) is generated from them by `make generate`. **Do not edit it by hand**; change the generator in [`internal/gen`](internal/gen/) instead. A test fails if the committed file differs from what the generator produces
- `./check-models.sh` (or `make models-check`) reports what is new, changed or removed upstream without modifying anything (add `--diff` to see which points changed); `make sync` downloads the models and regenerates the registry
- When you update models, review the diff of `registry/models_gen.go`: changed point names, types, offsets or scale factors can break callers, and belong in the release notes

### Errors

Return the package sentinels and `*DecodeError` so callers can use `errors.Is` / `errors.As`, and wrap with `%w`. See [ERRORS.md](ERRORS.md). Transport and Modbus errors come from go-modbus and are passed through unchanged.

### Commit Messages

Use clear, concise commit messages:

```
component: short description

Optional longer explanation of the change, why it was made,
and any relevant context.
```

Examples:
- `decode: flag unimplemented acc64 points`
- `sunspecctl: add --count to poll-point`
- `models: sync with sunspec/models`

### Testing

- Add tests for new functionality in the appropriate `_test.go` file; the repository uses only the standard `testing` package
- Use [`testutil`](testutil/) for anything that talks to a device: it serves a SunSpec model chain over Modbus TCP
- Assert behaviour: decoded values, scale factors, the specific error — not just that the code ran
- Keep tests fast, deterministic and race-clean; CI runs them with `-race` on every Go version from 1.23 and a 120 s per-package timeout
- Coverage is enforced by `make check` and Codecov; new code should come with tests
- If your change affects what is read from a device, verify against real hardware when possible and say which device in the pull request

### Documentation

When you change **public API or behaviour**, update:

- Doc comments on the affected symbols
- [API.md](API.md) and [ERRORS.md](ERRORS.md), and [README.md](README.md) where it references the changed behaviour or `sunspecctl` commands and flags
- [RELEASE.md](RELEASE.md), calling out behaviour changes and anything that breaks existing callers

## Submitting Changes

1. Push your branch to your fork:
   ```sh
   git push origin feature/my-change
   ```
2. Open a Pull Request against the `main` branch
3. Describe what your change does and why
4. Reference any related issues

For larger changes, please open an issue first to discuss the approach.

## Reporting Issues

- Use GitHub Issues to report bugs or suggest features
- Include the go-sunspec version (or `sunspecctl version` output), the Go version and the operating system
- Provide the exact command or a minimal program that reproduces the problem
- For device problems, include the manufacturer and model, the transport (TCP, RTU, …) and the output of `sunspecctl detect` and `sunspecctl models`; `sunspecctl read --raw` shows the registers behind a wrongly decoded value
- Report security vulnerabilities privately: see [SECURITY.md](SECURITY.md)

## Project Structure

```
.                      Package sunspec: detect, discover, read, decode
registry/              Model metadata and lookups; models_gen.go is generated
models/                SunSpec JSON models from sunspec/models (do not edit)
internal/schema/       JSON model parsing
internal/gen/          Code generator: models/ → registry/models_gen.go
testutil/              SunSpec fixture server for tests
cmd/sunspecctl/        Command-line tool
sync-models.sh         Download the models from sunspec/models
check-models.sh        Report upstream model changes (read-only)
```

## License

By contributing to `go-sunspec`, you agree that your contributions will be licensed under the [MIT License](LICENSE). The model definitions in `models/` remain under the [Apache License 2.0](https://github.com/sunspec/models/blob/master/LICENSE) of their upstream source.
