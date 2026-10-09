# Errors: otfabric/go-sunspec

Canonical guide to how go-sunspec reports failures.
For method signatures, see [API.md](API.md). Transport and Modbus protocol errors
originate in [go-modbus](https://github.com/otfabric/go-modbus); this package wraps
or re-exports the ones that matter at the SunSpec layer.

## Quick reference

| Situation | How it is reported |
|-----------|--------------------|
| Device has no SunSpec marker | `ErrNotSunSpec` |
| Model ID not in discovery result | `ErrUnknownModel` |
| Named point missing after decode | `ErrPointNotFound` |
| Register slice too short / decode failure | `*DecodeError` wrapping `ErrDecode` |
| Multi-chunk register read failed mid-way | `ErrPartialRead` wrapping the Modbus cause |
| Model chain invalid / size limit | `ErrModelChainInvalid` / `ErrModelChainLimitExceeded` (from go-modbus) |
| Unsupported point type | Warning on `DecodedModel` (raw registers kept); not a hard error |
| Nil `*modbus.Client`, or a register range past address 65535 | Error wrapping `modbus.ErrUnexpectedParameters`; nothing is sent |
| `DecodeModel` with a nil schema | `*DecodeError` wrapping `ErrDecode` |
| Transport / timeout / Modbus exception | Underlying go-modbus error (often wrapped) |

Prefer `errors.Is` / `errors.As`:

```go
device, err := sunspec.Discover(ctx, client, opts)
if errors.Is(err, sunspec.ErrNotSunSpec) {
    // not a SunSpec device
}

dm, err := device.ReadModelByID(ctx, 101)
if errors.Is(err, sunspec.ErrUnknownModel) {
    // model 101 was not discovered
}

var dec *sunspec.DecodeError
if errors.As(err, &dec) {
    // dec.ModelID, dec.PointName, dec.Offset, dec.PointType
}
```

## Sentinels

| Sentinel | Meaning |
|----------|---------|
| `ErrNotSunSpec` | Detect/Discover found no SunSpec marker at probed bases |
| `ErrUnknownModel` | `ReadModelByID`: ID absent from `Device.Discovery.Models` |
| `ErrPointNotFound` | `ReadPoint`: name not present in the decoded model |
| `ErrDecode` | Decode failure (usually via `*DecodeError`) |
| `ErrPartialRead` | Chunked read returned early; some registers may be present |
| `ErrModelChainInvalid` | Re-export of `modbus.ErrSunSpecModelChainInvalid` |
| `ErrModelChainLimitExceeded` | Re-export of `modbus.ErrSunSpecModelChainLimitExceeded` |

`ErrUnknownModel` does **not** mean “no local schema”. Models without a compiled
schema still appear in discovery with `SchemaKnown == false` and can be read as
raw registers.

## Typed decode errors

```go
type DecodeError struct {
    ModelID   uint16
    PointName string
    Offset    int
    PointType string
    Message   string
    Err       error // usually ErrDecode
}
```

`DecodeError` implements `Unwrap`. Match with `errors.Is(err, sunspec.ErrDecode)`
or `errors.As(err, &dec)`.

Unsupported point types do not return `DecodeError`. The decoder keeps raw
registers and appends a warning string to `DecodedModel.Warnings`.

## Partial and best-effort reads

- `ReadAll` returns decoded models collected so far plus the first error
  encountered. Inspect both the slice and the error.
- `ReadModel` on a transport failure may return a partial `DecodedModel`
  (raw registers + warning) together with a non-nil error.
- `ErrPartialRead` wraps the underlying Modbus error with `%w`, so
  `errors.Is` against go-modbus sentinels still works.

## Inherited Modbus behaviour

Logging, metrics, retries, timeouts, and connection errors come from the
injected `*modbus.Client`. Configure them on `modbus.Config` before
`Discover` / `Open`. See the go-modbus error and observability docs.
