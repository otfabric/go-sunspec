# go-sunspec API reference

Public API of `github.com/otfabric/go-sunspec` (package `sunspec`),
`github.com/otfabric/go-sunspec/registry` and
`github.com/otfabric/go-sunspec/devicefile`.

Errors: [ERRORS.md](ERRORS.md). Release notes: [RELEASE.md](RELEASE.md).

## Ownership and lifecycle

| Object | Owner | Notes |
|--------|-------|-------|
| `*modbus.Client` | Caller | Create with `modbus.New`, open/close explicitly |
| `*sunspec.Device` | Caller | Holds a reference to the client; does **not** close it |
| `*sunspec.Server` | Caller | A device in memory and a `modbus.RequestHandler`; it owns no listener |
| `*modbus.Server` | Caller | Create with `modbus.NewServer(conf, srv)`, start/stop explicitly (or let `Server.Serve` do it) |
| `registry` package data | Package init | Populated by generated `init`; do not `Register` after init |

`Open` is an alias of `Discover`. It does not open the Modbus connection;
call `client.Open()` first.

```go
client, err := modbus.New(modbus.Config{URL: "tcp://192.168.1.100:502"})
if err != nil {
    return err
}
if err := client.Open(); err != nil {
    return err
}
defer client.Close()

device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{UnitID: 1})
```

### A read-only client

The client side of this package reads registers: `Detect`, `Discover`,
`Open` and every `Device` method except `WritePoint` send Read Holding
Registers (FC 03) or Read Input Registers (FC 04), depending on
`DiscoverOptions.RegType`, and nothing else. `WritePoint` (since v0.10.0) is
the one method that writes ([Writing a point](#writing-a-point)).
`ReadFunctionCodes` returns the two read codes:

```go
func ReadFunctionCodes() []modbus.FunctionCode // FC 03 and FC 04; a new slice each call
```

Give them to `modbus.Config.AllowedFunctionCodes` (go-modbus v1.5.0) for a
client that cannot write, whoever uses it:

```go
client, err := modbus.New(modbus.Config{
    URL:                  "tcp://192.168.1.100:502",
    AllowedFunctionCodes: sunspec.ReadFunctionCodes(),
})
```

Such a client refuses every other function code (writes, coils, diagnostics,
device identification) with an error wrapping
`modbus.ErrFunctionCodeNotAllowed` before a byte is sent. Everything in this
package except `WritePoint` works through it unchanged; `WritePoint` then fails
with that error. The restriction belongs to the client:
`Discover` and `Device` take any `*modbus.Client` and do not check it.

## Concurrency

- `Device` methods are safe for concurrent use to the same extent as the
  underlying `*modbus.Client` (see go-modbus concurrency docs).
- `Server` methods are safe for concurrent use with each other and with the
  requests being served. Every update and every accepted client write replaces
  a model's registers atomically. No callback runs with the `Server`'s lock
  held (since v0.11.0): see [the OnWrite contract](#the-onwrite-contract) and
  [Updates](#updates).
- `registry.ByID`, `Known`, `All`, and `Count` are safe for concurrent reads
  after package initialization.
- Do not call `registry.Register` after init.

## Observability (inherited)

This package does not define loggers or metrics. Configure the injected client:

```go
client, err := modbus.New(modbus.Config{
    URL:     "tcp://192.168.1.100:502",
    Logger:  modbus.NopLogger(), // or a real logger
    // Metrics: ...,
    // RetryPolicy: ...,
})
```

Retries, timeouts, pooling, and logging behave exactly as documented by go-modbus.

## Detection and discovery

```go
func Detect(ctx context.Context, client *modbus.Client, opts *DiscoverOptions) (*DetectionResult, error)
func Discover(ctx context.Context, client *modbus.Client, opts *DiscoverOptions) (*Device, error)
func Open(ctx context.Context, client *modbus.Client, opts *DiscoverOptions) (*Device, error) // alias of Discover
```

`DiscoverOptions` fields: `UnitID`, `RegType`, `BaseAddresses`, `MaxModels`,
`MaxAddressSpan`. Nil opts use go-modbus SunSpec defaults.

The chain walk stops, without error, after `MaxModels` headers (256 by
default). When it has not reached the end marker by then,
`Device.Discovery.Warnings` says so ("model chain not terminated"): the device
has more models, or what was walked is not a model chain.

## Device reads

```go
func (d *Device) ReadAll(ctx context.Context) ([]*DecodedModel, error)
func (d *Device) ReadModel(ctx context.Context, inst ModelInstance) (*DecodedModel, error)
func (d *Device) ReadModelByID(ctx context.Context, modelID uint16) (*DecodedModel, error)
func (d *Device) ReadModels(ctx context.Context, insts []ModelInstance) ([]ModelResult, error)
func (d *Device) ReadPoint(ctx context.Context, inst ModelInstance, pointName string) (*DecodedPoint, error)
func (d *Device) WritePoint(ctx context.Context, inst ModelInstance, name string, value any) (*DecodedPoint, error) // since v0.10.0
func (d *Device) ModelByID(id uint16) *ModelInstance
func (d *Device) ModelsByID(id uint16) []ModelInstance
```

`ModelByID` returns a pointer to the first instance in `Discovery.Models`.
`ModelsByID` returns copies of every instance of the ID, in chain order (for
example one model 160 per MPPT tracker set), or nil when there is none;
changing them does not change the discovery result.

`ReadModel` (and everything built on it) reads the model's two header
registers together with its data and compares them with the discovery result.
When the device no longer reports that ID and length at that address the read
fails with `ErrModelChanged`; the returned `DecodedModel` holds the data
registers, undecoded. A model that needs several requests is read from its
header address in chunks of 125 registers.

### Writing a point

```go
var ErrPointNotWritable = errors.New("sunspec: point not writable")

p, err := dev.WritePoint(ctx, *dev.ModelByID(123), "WMaxLimPct", 75.0) // percent, as WMaxLimPct_SF scales it
```

`WritePoint` writes one point of the top-level group of a model instance and
returns the point as the device reports it afterwards. It reads the model
first: the header is checked as by `ReadModel` (`ErrModelChanged`), and the
value is encoded with the device's current scale factor, as `EncodeModel`
encodes it ([Values by point type](#values-by-point-type)): the engineering
value of a scaled point, a symbol name, bitfield names, a string, or
`[]uint16` with the point's registers. It then sends the point's registers,
and only those, in one Write Multiple Registers request (FC 16), and reads the
model again.

| Refused before anything is sent | Error |
|---|---|
| Schema access not `RW`, the count point of a group (nested at any depth), a model without a schema or with more registers on the device than the schema describes, a `Device` on input registers | `ErrPointNotWritable` |
| A value other than `[]uint16`, nil, an enum symbol name or a list of bitfield symbol names for a point whose scale factor point the device does not report as a usable value: not implemented, or outside -10..10 (since v0.11.0) | `ErrPointNotWritable`, with the reason |
| A name that is not a point of the top-level group | `ErrPointNotFound` |
| A value the encoder refuses | `*EncodeError` (`ErrEncode`, `ErrValueOutOfRange`) |
| A client built with `AllowedFunctionCodes: ReadFunctionCodes()` | `modbus.ErrFunctionCodeNotAllowed` |

A device that refuses the write returns its Modbus exception
(`modbus.ErrIllegalDataAddress`, say). The read before and the write are two
requests: a device that changes the scale factor in between gets the value
scaled with the old one. Points of repeating groups cannot be written.

A point whose schema names a scale factor point takes an engineering value
only while the device reports that scale factor as a usable value: without
one, the value cannot be converted to the device's registers, and
`WritePoint` refuses it before anything is sent. `Device.ReadModel` shows the
same condition as a point without `ScaledValue`. A `[]uint16` value is the
point's registers and is written as it is, whatever the scale factor reads,
and so is a nil value, which sets the point to its not-implemented value. Enum
symbol names and bitfield symbol lists are never scaled and are written
whatever the scale factor reads (since v0.12.0):

```go
_, err := dev.WritePoint(ctx, inst, "WMaxLimPct", 75.0)
if errors.Is(err, sunspec.ErrPointNotWritable) {
    // "... WMaxLimPct of model 123 is scaled by WMaxLimPct_SF, which the device reports as not implemented; ..."
    _, err = dev.WritePoint(ctx, inst, "WMaxLimPct", []uint16{750}) // the registers, as the caller knows them
}
```

Before v0.11.0 such a value was stored unscaled (75 here) and `WritePoint`
returned nil. A literal scale factor of the schema is always usable. See
[SPECIFICATIONS.md](SPECIFICATIONS.md#scale-factors).

### Reading several models

```go
type ModelResult struct {
    Instance ModelInstance // as given
    Model    *DecodedModel // as ReadModel returns it; nil when nothing of it was read
    Err      error         // as ReadModel returns it, or wrapping batch.ErrNotAttempted
}
```

`ReadModels` reads the given instances with fewer requests than `ReadModel`
when they are adjacent in the chain, as `Discovery.Models` are: adjacent
instances share a request up to 125 registers, and no register outside them is
read. It plans the requests with the go-modbus
[`batch`](https://github.com/otfabric/go-modbus/blob/main/API.md#12-batch-reads-package-batch)
package. Each request includes the headers of the instances it serves, and
each header is checked as `ReadModel` checks it. An instance with `Length` of
123 or less is never split across requests, so its values and scale factors
come from one response. A longer one gets requests of its own, as in
`ReadModel`. An instance given twice is read once.

It returns one `ModelResult` per instance, in input order. The error is nil
when every instance succeeded; otherwise it wraps the first failure, and each
result carries its own:

| What happens | Instances affected | Result |
|---|---|---|
| A Modbus exception on a request serving several instances | those instances | each is read again on its own, so it gets the result `ReadModel` gives it |
| A Modbus exception on a request of one instance | that instance | as `ReadModel`: the exception, or `ErrPartialRead` with the registers read for a long model |
| A timeout or transport error | the instances of that request | that error; the instances after it are not read. A long instance the batch stops in the middle of keeps the registers already read in `Model`, and `Err` wraps `ErrPartialRead` and `batch.ErrNotAttempted` (since v0.12.0; before, `Model` was nil) |
| A cancelled or expired context | the instances not yet read | `Err` wrapping `batch.ErrNotAttempted` and the context error; `Model` nil for an instance of which nothing was read, the registers already read with `ErrPartialRead` for a long one the batch stopped in the middle of |

The requests are made one after another; they are not an atomic snapshot of
the device. `ReadAll` is unchanged and reads one model per request; to read the
whole chain with fewer requests, call `d.ReadModels(ctx, d.Discovery.Models)`.
On the benchmark inverter chain (models 1, 103, 120–123) that takes 3 requests
instead of 6.

Offline decode (no I/O):

```go
func DecodeModel(regs []uint16, meta *registry.ModelMeta, instanceAddr uint16) (*DecodedModel, error)
```

### Groups

A model is a tree of groups, in the schema and in the decoded result.

```go
type DecodedModel struct {
    ModelID, InstanceAddress uint16
    Name         string
    Schema       *registry.ModelMeta
    Group        *DecodedGroup // top-level group; nil when the model was not decoded
    RawRegisters []uint16
    Warnings     []string
}

type DecodedGroup struct {
    Name   string          // group name from the schema
    Index  int             // 0 for the top-level group, 1..N for nested instances
    Points []DecodedPoint  // the group's own points
    Groups []*DecodedGroup // nested instances, in register order
}

func (m *DecodedModel) Point(name string) *DecodedPoint          // first match anywhere, or nil
func (g *DecodedGroup) Point(name string) *DecodedPoint          // own points only
func (g *DecodedGroup) FindPoint(name string) *DecodedPoint      // own points, then nested, depth first
func (g *DecodedGroup) GroupsNamed(name string) []*DecodedGroup  // nested instances of one group
func (g *DecodedGroup) Walk(fn func(*DecodedGroup))              // the group, then everything nested
```

The registers of a model are consumed in tree order: a group's own points,
then all instances of its first nested group, then of the second, and so on,
each instance again followed by its own nested groups. The number of
instances of a nested group is, per the schema (`registry.GroupMeta`):

| `GroupMeta` | Instances |
|---|---|
| `CountPoint` set | The value of that point, looked up in the nearest enclosing group instance. A missing or unimplemented count point gives no instances and a warning. |
| `Count` > 0 | That many (a group without a count in the model definition occurs once). |
| `Count` == 0 | As many as the remaining registers hold. |

A named scale factor is looked up the same way: in the point's own group
instance first, then in the enclosing ones. The SunSpec information model gives
`sunssf` the range -10 to 10: a point whose nearest implemented scale factor is
outside it is left unscaled (`ScaledValue` and `SFRawValue` nil) and the model
gets a warning; before v0.9.0 the exponent was applied, so 10^400 made the value
+Inf, or NaN for a raw 0. A literal exponent in a schema outside -10..10 is
treated like an unresolvable name.

`DecodedPoint.RegisterOffset` is the point's offset from the model's ID
register, that is its index in `RawRegisters`.

### JSON encoding

`DecodedPoint` implements `json.Marshaler`. The field names are those of the
struct. A float `RawValue` or a `ScaledValue` that is NaN or infinite is encoded
as `null`: JSON cannot represent those, and a float point the device reports as
not implemented decodes to NaN (with `Implemented: false`).

### Not-implemented values

`DecodedPoint.Implemented` is false when the device reports the SunSpec
"not implemented" value for the point's type: `0x8000` for `int16` and `sunssf`,
`0xFFFF` for `uint16`, `enum16` and `bitfield16` (and the 32/64-bit
equivalents), `0` for accumulators, NaN for floats, all NUL for `string`, the
all-zero address for `ipaddr` and `ipv6addr`, and `FF:FF:FF:FF:FF:FF` for
`eui48`. `RawValue` then holds that sentinel and is not a measurement.

## Encoding

```go
type Values struct {
    Points map[string]any       // point name -> value; absent or nil = not implemented
    Groups map[string][]*Values // nested group name -> instances, in order
}

func (v *Values) Set(name string, value any) *Values  // chainable
func (v *Values) Add(group string, inst *Values) *Values // returns the added instance
func (v *Values) Clone() *Values

func EncodeModel(meta *registry.ModelMeta, values *Values) ([]uint16, error)
func (m *DecodedModel) Values() *Values
```

`EncodeModel` is the inverse of `DecodeModel`: it returns the whole model,
index 0 the ID register, index 1 the length register (set by the encoder),
then the data in the order of the schema's group tree. It performs no I/O.

### Values by point type

| Point type | Accepted values |
|---|---|
| `int16` … `uint64`, `acc16` … `acc64`, `count`, `sunssf`, `pad` | Any Go integer or float type, `json.Number` |
| `float32`, `float64` | Any Go integer or float type, `json.Number` |
| `enum16`, `enum32` | A number, or a `string` naming a symbol of the schema |
| `bitfield16` … `bitfield64` | A number, or a `[]string` naming the bits to set |
| `string` | `string`, at most two bytes per register; shorter values are NUL padded |
| `ipaddr`, `ipv6addr` | `string` such as `"192.168.1.10"` / `"fe80::1"` |
| `eui48` | `string` such as `"00:11:22:33:44:55"` |
| any, including unsupported types | `[]uint16` with exactly the point's registers, used as they are |

### Not implemented

A point that is absent from `Points`, or `nil`, is encoded as its type's "not
implemented" value: the values listed under
[Not-implemented values](#not-implemented-values), `0x8000` for `pad`, and
`0x0000FFFFFFFFFFFF` for `eui48`. Giving such a value explicitly for an integer,
enum or bitfield point (65535 for a `uint16`, NaN for a float) is an error, so
that a measurement cannot turn into "not implemented" by accident.
Accumulators accept 0.

### Scale factors, rounding, range

A point whose schema names a scale factor takes the **engineering value**. The
exponent is the value given for the `sunssf` point of that name in the same
group instance or, failing that, in an enclosing one — the lookup the decoder
uses — or the schema's literal exponent. The stored integer is
`value / 10^sf`, rounded to the nearest integer with halves away from zero.
Unscaled integer points round a fractional value the same way. When the
`sunssf` point is not set at any level, or holds raw registers outside -10..10,
the value is stored unscaled, which is how `DecodeModel` reads such a point. A
number outside -10..10 for a `sunssf` point fails with `ErrValueOutOfRange`
(since v0.9.0; raw registers given as `[]uint16` are stored as they are). Integer values without scaling are
converted exactly, including 64-bit ones beyond the precision of a `float64`.

Nothing is wrapped or clamped: a value outside the range of the point's type
after scaling fails with an error matching `ErrValueOutOfRange`. So does a
finite value of a float point that scaling would turn into ±Inf (since
v0.12.0); an infinite value is taken as given.

### Groups and counts

How many instances of a nested group are encoded follows the schema, as in
[Groups](#groups):

| `GroupMeta` | Instances encoded |
|---|---|
| `CountPoint` set | The value given for the count point; when it is not set, the largest number of instances given for any group it counts. The count point is encoded with that number. Groups with fewer instances are completed with not-implemented ones; more instances than an explicit count is an error. |
| `Count` > 0 | That many, completed the same way. |
| `Count` == 0 | As many as given. |

### Values of a decoded model

`DecodedModel.Values()` returns what `EncodeModel` needs to rebuild the model:
implemented points only, engineering values where a non-zero scale factor was
resolved and raw values otherwise, enum and bitfield points as numbers, every
group instance. Where a value would not read back the same (a 64-bit value a
`float64` cannot hold, a count point reported as not implemented) it gives the
point's registers. For a model that decoded without warnings,
`DecodeModel(EncodeModel(m.Schema, m.Values()))` yields the same points.

## Serving

```go
type ServerModel struct {
    ID        uint16
    Schema    *registry.ModelMeta // nil: the registry's schema for ID
    Values    *Values             // point values, encoded with the schema
    Registers []uint16            // or: raw data registers (vendor models)
}

type ServerOptions struct {
    UnitID      uint8          // 0 selects 1
    SilentToOtherUnits bool    // no answer, instead of exception 0x0B, for other unit IDs (since v0.5.1)
    BaseAddress uint16         // address of the "SunS" marker; zero value is address 0
    RegType     modbus.RegType // HoldingRegister (default) or InputRegister
    OnWrite     func(ctx context.Context, w *ServerWrite) error
}

func NewServer(opts *ServerOptions, models ...ServerModel) (*Server, error)

func (s *Server) UnitID() uint8
func (s *Server) BaseAddress() uint16
func (s *Server) RegType() modbus.RegType
func (s *Server) Models() []ModelHeader             // layout, as discovery finds it
func (s *Server) Model(index int) (*DecodedModel, error) // snapshot, decoded

func (s *Server) SetPoint(index int, name string, value any) error
func (s *Server) SetValues(index int, values *Values) error
func (s *Server) Update(index int, fn func(v *Values) error) error
func (s *Server) SetRegisters(index int, data []uint16) error

func (s *Server) Serve(ctx context.Context, conf *modbus.ServerConfig) error

// modbus.RequestHandler
func (s *Server) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error)
func (s *Server) HandleInputRegisters(ctx context.Context, req *modbus.InputRegistersRequest) ([]uint16, error)
func (s *Server) HandleCoils(ctx context.Context, req *modbus.CoilsRequest) ([]bool, error)
func (s *Server) HandleDiscreteInputs(ctx context.Context, req *modbus.DiscreteInputsRequest) ([]bool, error)
```

`index` is the position of a model in the chain, starting at 0: a device can
hold several models with the same ID.

### Layout

`BaseAddress` holds the marker (`0x5375`, `0x6E53`), followed by each model (ID
register, length register, data) and the end marker (`0xFFFF`, `0`). The layout
is fixed when the `Server` is created; `NewServer` rejects, with
`ErrModelLayout`, a model without data registers, a model ID 65535, and a chain
that does not fit below address 65536. A device without models is valid.

### Requests

| Request | Answer |
|---|---|
| Read of the configured register type, range inside the device map (it may span models) | The registers |
| Read touching any address outside the map | Exception 2, illegal data address |
| Read or write of the other register type | Exception 2 |
| Any request for another unit ID | Exception 0x0B, gateway target device failed to respond. With `SilentToOtherUnits`: no answer (`modbus.ErrNoResponse`), the client times out, as with a device on a serial line |
| Any request for unit ID 0 (broadcast) | No answer, and the request is not carried out (since v0.5.1; v0.5.0 answered exception 0x0B) |
| Coils, discrete inputs | Exception 1, illegal function |
| FC 22 (mask write), FC 23 (read/write) | Exception 1 (go-modbus answers: the optional handlers are not implemented) |

### Client writes

A write (FC 6, FC 16) is accepted when it covers whole points and nothing else,
all with schema access `RW`, in a model served from `Values`. Everything else is
refused with exception 2 and changes nothing: read-only points, the marker, ID
and length registers, the count point of a group (whatever its access: it
defines the layout), part of a multi-register point, a write with any register
that belongs to no point (also when a caller's schema has overlapping points),
a model served from `Registers`, the end marker, unmapped addresses. With `RegType`
`InputRegister` nothing is writable.

The `Server` does not judge values: the registers of an accepted write are
stored as they are, and from then on they are the model's values (a later
`SetPoint` on another point keeps them). `OnWrite`, when set, is called before
a write takes effect with the write decoded:

```go
type ServerWrite struct {
    ClientAddr string
    ModelIndex int
    ModelID    uint16
    Address    uint16         // first register written
    Registers  []uint16       // values written
    Points     []DecodedPoint // the written points, as they will be
    Model      *DecodedModel  // the whole model, as it will be
}
```

Returning an error refuses the write; the client receives it as a Modbus
exception (`modbus.ErrIllegalDataValue`, say; an error that is not a Modbus
exception becomes a server device failure).

#### The OnWrite contract

Since v0.11.0 `OnWrite` runs without the `Server`'s lock held. Before, it ran
with the `Server` locked and must not call its methods.

| Question | Answer |
|---|---|
| Which goroutine | The go-modbus goroutine that serves the client's connection. Requests of other connections, reads and writes, are served while `OnWrite` runs; the next request of the same connection waits. Writes on different connections call `OnWrite` at the same time: an `OnWrite` that keeps state of its own protects it (before v0.11.0 the `Server`'s lock made the calls take turns) |
| May it block | Yes. `ctx` is the connection's context: go-modbus cancels it when the connection ends or the server shuts down |
| Which `Server` methods may it call | All of them. `UnitID`, `BaseAddress`, `RegType` and `Models` never wait; `Model` returns the model as it is before the write. An update of the model being written (`SetPoint`, `SetValues`, `Update`, `SetRegisters`) makes the write fail, see the next row |
| When does the write take effect | When `OnWrite` returns nil and the model has not changed since the write was checked. When another client write or an update changed that model in between, the write is refused with `modbus.ErrServerDeviceFailure` (exception 4) and nothing changes: `OnWrite` may then have seen a write that did not take effect. Changes of other models do not matter |
| May it change the `ServerWrite` | Yes, to no effect: `Registers`, `Points` and `Model` (its `RawRegisters` included) are copies that belong to `OnWrite`. What is stored is what the client wrote |
| What a panic does | Behind a go-modbus server (`Serve`, `modbus.NewServer`), go-modbus v1.16.1 recovers it, logs it with its stack at error level and answers exception 4; the write does not take effect and the `Server` keeps serving. A direct caller of `HandleHoldingRegisters` gets the panic |

A `Server` without `OnWrite` applies a write in one step, as before.

### Updates

`SetPoint` sets one top-level point, `SetValues` replaces all values of a
model, `Update` hands a copy of the current values to a function and encodes
what it leaves, `SetRegisters` replaces the data registers (of any model; for a
model served from values they must describe the same group structure). Each is
atomic for a reader: one Modbus request never returns half an update. A model
longer than 125 registers takes several requests and can show different
updates in each, as with any Modbus device. Nothing changes when an update
returns an error. An update cannot change the length of a model
(`ErrModelLayout`).

The function given to `Update` runs on the goroutine that called `Update`,
without the `Server`'s lock held (since v0.11.0; before, it ran once, with the
`Server` locked, and must not call its methods):

- Requests are served while it runs, updates made on other goroutines run
  their functions at the same time, and it may call the `Server`'s methods to
  read (`Models`, `Model`).
- When the model changes while it runs (a client write, another update),
  `Update` calls it again with a copy of the new values, and stores only the
  result of the last call. So it may be called more than once and must do
  nothing but change its `v`: no counting, no I/O that must happen once. A
  function that itself changes the same model through the `Server` each time
  it is called makes `Update` call it for ever, and a slow function on a model
  that others change all the time is called again each time.
- `v` belongs to that call and is valid only until the function returns: what
  it leaves in `v` becomes the model's values. Keep `v.Clone()` to use the
  values later; changing `v` itself after the return is a data race with the
  `Server`.
- A panic in the function reaches the caller of `Update`, and nothing changes.

`SetPoint` and `SetValues` are `Update` with a function of their own and
behave the same; `SetRegisters` calls no function.

### Listening

The `Server` does not listen. Pass it to `modbus.NewServer`, which owns the
listener, connections, idle timeout, client limit, TLS, logging and metrics.
go-modbus serves `tcp://` and `tcp+tls://`. `Server.Serve(ctx, conf)` is a
convenience that starts such a server, waits for `ctx` and shuts it down
(allowing connections five seconds to end). When a request is still being
served after those five seconds (an `OnWrite` that does not return, say),
`Serve` returns an error matching `context.DeadlineExceeded` and does not wait
for it: the listener and the connections are closed by then.

## Device files

Package `github.com/otfabric/go-sunspec/devicefile` reads and writes *device
files*: JSON documents that describe a SunSpec device by its models and their
point values. It is the format `sunspecctl serve` runs, `sunspecctl capture`
and `sunspecctl template` write and the OT Fabric SunSpec Explorer exports
(the package was moved here from sunspecctl in v0.6.0).

```json
{
  "unit_id": 1,
  "base_address": 40000,
  "models": [
    {
      "id": 1,
      "points": { "Mn": "Acme", "Md": "PV-5000", "SN": "A-0001" }
    },
    {
      "id": 103,
      "points": {
        "A": 18.3, "A_SF": -1,
        "PhVphA": 230.4, "V_SF": -1,
        "W": 4200, "W_SF": 0,
        "Hz": 50.01, "Hz_SF": -2,
        "St": "MPPT",
        "Evt1": ["GROUND_FAULT"]
      }
    },
    {
      "id": 160,
      "points": { "DCW_SF": 0 },
      "groups": {
        "module": [
          { "points": { "ID": 1, "DCW": 2150 } },
          { "points": { "ID": 2, "DCW": 2050 } }
        ]
      }
    },
    { "id": 64900, "registers": [1, 2, 3] }
  ]
}
```

| Member | Meaning |
|---|---|
| `unit_id` | Modbus unit ID, 1–255. Default 1. |
| `base_address` | Register address of the `SunS` marker. Default 40000. |
| `models` | The models in chain order. Each has an `id` and either `points` (and `groups`) or `registers`. A model ID may occur more than once. |
| `points` | Point values by SunSpec point name. A point that is left out is not implemented. |
| `groups` | Repeating groups: group name → list of instances, each with its own `points` and `groups`. Count points such as `NPt` are filled in from the number of instances. |
| `registers` | The model's data registers (what follows the ID and length registers), 0–65535 each. For models without a schema, and for anything to be served bit for bit. |
| `name` | Ignored when the file is used; written for the reader. |

A point value is what [`Values`](#values-by-point-type) takes, in JSON: a
number (the engineering value for a point with a scale factor), a string (text,
an enum symbol name, an address), a list of bitfield symbol names, a list of
raw registers for any point, or `null` for "not implemented". A list holds at
most 65535 values (`maxItems` in the schema), as no point has more registers.
Unknown members, a member given twice or in another case, a point or nested
group given twice, and point names the model does not have are errors.

`devicefile.Schema()` returns a JSON Schema (draft 2020-12) of the format, the
file [`devicefile/devicefile.schema.json`](devicefile/devicefile.schema.json),
`$id` `https://otfabric.io/schemas/sunspec/devicefile-1.schema.json`. It
describes the form of a file; whether models and points exist and values fit
is checked when the device is built.

```go
const DefaultUnitID = 1
const DefaultBaseAddress = 40000
const SchemaID = "https://otfabric.io/schemas/sunspec/devicefile-1.schema.json"

type File struct {
    UnitID      *uint8  // nil: DefaultUnitID
    BaseAddress *uint16 // nil: DefaultBaseAddress
    Models      []Model
}

type Model struct {
    ID        *uint16  // nil: "id" is missing, an error when the file is used
    Name      string   // for the reader
    Group              // the points and groups of the top-level group
    Registers []uint16 // or: raw data registers
}

type Group struct {
    Points Points
    Groups map[string][]*Group // nil instance: an empty one
}

type Point struct {
    Name  string
    Value interface{} // parsed: json.Number, string, []interface{}, nil
}

type Points []Point // one JSON object, in order

func New(unitID uint8, baseAddress uint16) *File // no models: "models": []
func Parse(data []byte) (*File, error)
func Load(path string) (*File, error)      // regular files of up to MaxFileSize only

const MaxFileSize = 16 << 20                 // 16 MiB, since v0.9.0

func (f *File) Marshal() ([]byte, error)      // indented by two spaces, ends with a newline
func (f *File) Write(w io.Writer) error
func (f *File) WriteFile(path string) error   // temp file, fsync, rename; mode 0600

func (f *File) ServerModels() ([]sunspec.ServerModel, error)
func (f *File) Server(opts *ServerOptions) (*sunspec.Server, error)
func (g *Group) Values() (*sunspec.Values, error)

type ServerOptions struct {
    UnitID             *uint8  // replaces the file's
    BaseAddress        *uint16 // replaces the file's
    SilentToOtherUnits bool
    RegType            modbus.RegType
    OnWrite            func(ctx context.Context, w *sunspec.ServerWrite) error
}

func Capture(ctx context.Context, device *sunspec.Device, raw bool) (*File, error)
func FromDecoded(dm *sunspec.DecodedModel, raw bool) Model
func Template(meta *registry.ModelMeta) Model

func Schema() []byte
```

### Reading a file

`Parse` checks the form of the file: JSON, the types of the members, no
unknown members, no member given twice, no member name in another case
(`"ID"` for `"id"`), no point given twice, no nested group named twice in one
`"groups"` object, no list or object of more than 65535 entries at any depth
of a point value, nothing after the document (the member, case, group and
length checks since v0.12.0). Its errors are `*SyntaxError` (matching
`ErrSyntax`), with line and column for malformed JSON, for a member of the
wrong type, and for a member given twice, a member name in another case and
a nested group named twice. `Load` refuses a path that is not a regular file
(`ErrNotRegular`: a directory, `/dev/zero`, a named pipe; a symbolic link to a
regular file is followed) and a file longer than `MaxFileSize`, 16 MiB
(`ErrTooLarge`), before parsing anything. `WriteFile` writes a temporary file
of mode 0600 in the destination's directory, syncs it and renames it into
place, so a failure leaves the old file as it was; it refuses a destination
that is a symbolic link (dangling or not) or another non-regular file with
`ErrNotRegular` and does not follow it. Before v0.9.0 `Load` read any path
whole, and `WriteFile` followed links, truncated before writing and used mode
0644. `Load` reads a file and prefixes a parse error with the
path. The order of the points is kept (`Points`), so that a file is written
back the way it was read.

`File.Server` builds the [`Server`](#serving) the file describes, which
validates every value against the model's schema, and the layout:

```go
file, err := devicefile.Load("device.json")
if err != nil {
    return err
}
srv, err := file.Server(nil) // or &devicefile.ServerOptions{UnitID: &id, OnWrite: hook}
if err != nil {
    return err
}
return srv.Serve(ctx, &modbus.ServerConfig{URL: "tcp://127.0.0.1:1502"})
```

The device answers the file's unit ID at the file's base address, 1 and 40000
when the file does not say; `ServerOptions.UnitID` and `BaseAddress` replace
them. Unit ID 0 is refused. `File.ServerModels` gives the models alone, for
your own `sunspec.NewServer` call. It converts the groups of a model along the
model's schema (since v0.11.0): a nested group the schema does not have at
that place is an error, found before its instances are converted, and so are
more than 65535 group instances in one model, which no model holds. The
values themselves are checked when the device is built. How much memory a
file takes: [LIMITATIONS.md](LIMITATIONS.md#memory). Errors name the model by position and ID,
the group instance and the point (`*ModelError`, or the `*sunspec.EncodeError`
of the encoder): see [ERRORS.md](ERRORS.md#device-files).

### Writing a file

`Capture` reads every model of a discovered `Device` and describes it;
`FromDecoded` does so for one model you read yourself. A model that decoded
without warnings is described by its point values, in schema order, which a
person can read and edit; a model without a schema, one that is shorter or
longer than its schema, and one whose values would not read back the same, by
its `registers`. A model is described by its values only when every
implemented point decodes back exactly from them, so a string point that is
not valid UTF-8 (which JSON cannot hold) makes the model `registers` (since
v0.12.0). With `raw`, every model is described by its registers. Without
`raw`, serving the file gives a device that decodes the same as the original,
not one identical register for register: the space padding of strings and
the registers of points that are not implemented may differ. With `raw` it is
identical register for register. A model read with no data registers is
written as `"registers": []`, which `File.Server` refuses with
`ErrModelLayout` (since v0.12.0; before, it was served at its schema's
length). Enum and bitfield
points are written as numbers, so that values the schema does not name
survive.

`Capture` is all or nothing: a model that does not fit its schema
(`ErrDecode`) is still captured, from its registers; any other read error
ends it with `read model <id>: <cause>` and no file.

`Template` describes a model with every point present and `null`, and one
instance of each nested group: a skeleton to fill in.

```go
file, err := devicefile.Capture(ctx, device, false)
if err != nil {
    return err
}
err = file.WriteFile("device.json")

skeleton := devicefile.New(devicefile.DefaultUnitID, devicefile.DefaultBaseAddress)
skeleton.Models = append(skeleton.Models, devicefile.Template(registry.ByID(103)))
err = skeleton.Write(os.Stdout)
```

A captured file is a snapshot and can hold serial numbers and network
addresses of the device.

## Registry

```text
func registry.ByID(id uint16) *ModelMeta
func registry.Known(id uint16) bool
func registry.Count() int
func registry.All() map[uint16]*ModelMeta // shallow copy of the map
func registry.Register(m *ModelMeta)      // for generated init only
```

`ModelMeta.Group` is the model's top-level `*GroupMeta`. A `GroupMeta` has the
group's `Name`, `Label`, `Desc` and `Type` (`"group"` or `"sync"`), its own
`Points` and their total `Length` in registers, the nested `Groups`, and
`Count` / `CountPoint` as described under [Groups](#groups).
`ModelMeta.FixedLength()` is the length of the top-level group's own points,
including the ID and L registers.

## Core types

- `Device` — client + unit + discovery result; `WritePoint` the one write
- `ErrPointNotWritable` — a write `WritePoint` refuses (since v0.10.0)
- `DiscoverOptions`, `DetectionResult`, `DiscoveryResult`, `ModelInstance`
- `DecodedModel`, `DecodedGroup`, `DecodedPoint`
- `DecodeError` — typed decode failure (see ERRORS.md)
- `Values`, `EncodeError` — values for encoding, typed encode failure
- `Server`, `ServerOptions`, `ServerModel`, `ServerWrite` — a served device
- `ReadFunctionCodes` — the function codes of a read-only client
- `devicefile.File`, `Model`, `Group`, `Points`, `Point` — a device file;
  `devicefile.SyntaxError`, `ModelError` — its typed errors
