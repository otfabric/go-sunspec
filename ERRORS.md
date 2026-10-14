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
| Device no longer has the discovered model at that address | `ErrModelChanged` |
| Chain longer than `MaxModels` | No error; warning in `Device.Discovery.Warnings` |
| Model chain invalid / size limit | `ErrModelChainInvalid` / `ErrModelChainLimitExceeded` (from go-modbus) |
| Unsupported point type | Warning on `DecodedModel` (raw registers kept); not a hard error |
| Nil `*modbus.Client`, or a register range past address 65535 | Error wrapping `modbus.ErrUnexpectedParameters`; nothing is sent |
| `DecodeModel` with a nil schema | `*DecodeError` wrapping `ErrDecode` |
| Transport / timeout / Modbus exception | Underlying go-modbus error (often wrapped) |
| Value cannot be encoded (unknown point, wrong Go type, bad symbol) | `*EncodeError` matching `ErrEncode` |
| Value does not fit its point after scaling, or equals the "not implemented" value | `*EncodeError` matching `ErrEncode` and `ErrValueOutOfRange` |
| Point name not in the schema's group (`EncodeModel`, `Server.SetPoint`) | `*EncodeError` matching `ErrEncode` and `ErrPointNotFound` |
| Models do not fit the address space; update would change a model's length | `ErrModelLayout` |
| `Server` model index out of range; `ServerModel` without registers and without schema | `ErrUnknownModel` |
| Client write the `Server` refuses | Modbus exception 2 to the client; no Go error |
| Function code a read-only client does not allow | Error wrapping `modbus.ErrFunctionCodeNotAllowed` (go-modbus); nothing is sent. The reads of this package are never refused by `ReadFunctionCodes()` |
| Device file is not valid JSON, has an unknown member, a wrong type, a point twice | `*devicefile.SyntaxError` matching `devicefile.ErrSyntax` |
| Device file model without ID, with registers and points, without schema; unsupported kind of value; unit ID 0 | `*devicefile.ModelError` (or a plain error for the unit ID) matching `devicefile.ErrInvalid` |
| Device file value that does not fit its point, or models that do not fit | `*EncodeError` / `ErrModelLayout`, as from `NewServer` |

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
| `ErrModelChanged` | `ReadModel`: the ID and length registers read with the model differ from the discovery result; discover again |
| `ErrEncode` | Encode failure (via `*EncodeError`) |
| `ErrValueOutOfRange` | A value does not fit its point: out of the type's range after scaling, equal to the type's "not implemented" value, a string longer than the point, more group instances than allowed. Always together with `ErrEncode` |
| `ErrModelLayout` | `NewServer`: a model without data registers, model ID 65535, or a chain past address 65535. `Update`, `SetValues`, `SetPoint`, `SetRegisters`: the change would alter the length or group structure of a served model |
| `ErrModelChainInvalid` | Re-export of `modbus.ErrSunSpecModelChainInvalid`: a zero-length model that is not the end marker, or a model that does not fit in the address space (it runs beyond address 65535, or leaves no room for the header after it) |
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

## Typed encode errors

```go
type EncodeError struct {
    ModelID   uint16
    Group     string // e.g. "DERVoltVar/Crv[2]/Pt[1]"; empty for the model as a whole
    PointName string
    PointType string
    Message   string
    Err       error // ErrValueOutOfRange, ErrPointNotFound, or nil
}
```

`errors.Is(err, sunspec.ErrEncode)` holds for every `EncodeError`, whatever its
cause; `errors.Is` also matches the cause. `EncodeModel`, `NewServer` and the
`Server` update methods return it.

```go
err := srv.SetPoint(1, "W", 400000)
var enc *sunspec.EncodeError
if errors.As(err, &enc) && errors.Is(err, sunspec.ErrValueOutOfRange) {
    // enc.PointName == "W", enc.PointType == "int16"
}
```

`NewServer` prefixes the error with the position of the model in the chain.
Encoding never clamps or wraps a value, and an error leaves a served model as
it was.

## Serving: what the client sees

A `Server` reports problems to Modbus clients as exceptions, not as Go errors:

| Request | Exception |
|---------|-----------|
| Read or write touching an address outside the device map | 2, illegal data address |
| Write to a read-only point, a header, the marker, a count point, part of a point, a raw model | 2, illegal data address |
| Request for another unit ID | 0x0B, gateway target device failed to respond; no answer at all with `ServerOptions.SilentToOtherUnits` (since v0.5.1) |
| Request for unit ID 0 (broadcast) | No answer; the request is not carried out (since v0.5.1) |
| Coils, discrete inputs, FC 22, FC 23 | 1, illegal function |
| Write refused by `ServerOptions.OnWrite` | The error it returned; server device failure (4) when that is not a Modbus exception |

## Device files

Package `devicefile` has its own sentinels and types. Their messages do not
start with a package name: they are written to follow a file name, as in
`device.json: line 3, column 14: invalid character ...`.

| Sentinel | Meaning |
|----------|---------|
| `devicefile.ErrSyntax` | `Parse`, `Load`: the text is not a device file. Always through `*SyntaxError` |
| `devicefile.ErrInvalid` | `File.ServerModels`, `File.Server`: the file parsed but does not describe a device. Through `*ModelError` for a problem of one model; a plain error for unit ID 0 |

```go
type SyntaxError struct {
    Line, Column int   // from 1; 0 when the position is not known
    Err          error // the encoding/json error, or a description
}

type ModelError struct {
    Index   int     // position in File.Models, from 0
    ModelID *uint16 // nil when the model has no "id"
    Err     error   // names the group instance and the point
}
```

`SyntaxError` formats as `line <l>, column <c>: <cause>` and has a position
for malformed JSON and for a member of the wrong type (`errors.As` finds the
`*json.SyntaxError` or `*json.UnmarshalTypeError`). An unknown member, a point
given twice, a truncated document and data after the document have none.
`ModelError` formats as `models[<index>] (model <id>): <problem>`, for example
`models[0] (model 160): group module[2]: point DCW: unsupported value true:
use a number, a string, a list or null`.

`File.Server` also returns what `NewServer` returns: `*EncodeError` (matching
`ErrEncode`, and `ErrValueOutOfRange` or `ErrPointNotFound`) for a value or a
name the model's schema refuses, `ErrModelLayout` for a model with an empty
`registers` list or a chain that does not fit the address space. They do not
match `devicefile.ErrInvalid`.

`Load` returns the operating system's error for a file it cannot read, and
`Capture` the read error prefixed with `read model <id>`; `errors.Is` matches
the cause (`ErrPartialRead`, `ErrModelChanged`, a go-modbus error, the
context's error). A nil device is an error wrapping
`modbus.ErrUnexpectedParameters`.

```go
file, err := devicefile.Load(path)
var syn *devicefile.SyntaxError
if errors.As(err, &syn) && syn.Line > 0 {
    // point the editor at syn.Line, syn.Column
}
```

## Partial and best-effort reads

- `ReadAll` returns decoded models collected so far plus the first error
  encountered. Inspect both the slice and the error.
- `ReadModel` on a transport failure may return a partial `DecodedModel`
  (raw registers + warning) together with a non-nil error.
- `ErrPartialRead` wraps the underlying Modbus error with `%w`, so
  `errors.Is` against go-modbus sentinels still works.
- A model that fails with `ErrModelChanged` is returned with the data
  registers that were read and no decoded group: they belong to whatever the
  device has at that address now.

go-modbus lets a context deadline override the client's `Timeout`. A call such
as `ReadAll` makes several requests under one context: when a response is lost,
the request waiting for it uses up the rest of the context's time, and the
models after it fail with the context's error. Give long multi-model reads a
context deadline that suits a single request, or none and rely on `Timeout`.

## Inherited Modbus behaviour

Logging, metrics, retries, timeouts, and connection errors come from the
injected `*modbus.Client`. Configure them on `modbus.Config` before
`Discover` / `Open`. See the go-modbus error and observability docs.

A client created with `modbus.Config.AllowedFunctionCodes` refuses other
function codes with an error wrapping `modbus.ErrFunctionCodeNotAllowed`
before anything is sent. With `sunspec.ReadFunctionCodes()` as the list, no
call of this package is ever refused: its client side sends FC 03 and FC 04
only (see [API.md](API.md#a-read-only-client)).
