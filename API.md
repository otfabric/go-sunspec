# go-sunspec API reference

Public API of `github.com/otfabric/go-sunspec` (package `sunspec`) and
`github.com/otfabric/go-sunspec/registry`.

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

## Concurrency

- `Device` methods are safe for concurrent use to the same extent as the
  underlying `*modbus.Client` (see go-modbus concurrency docs).
- `Server` methods are safe for concurrent use with each other and with the
  requests being served. Every update and every accepted client write replaces
  a model's registers atomically.
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
    // Retry: ...,
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
func (d *Device) ReadPoint(ctx context.Context, inst ModelInstance, pointName string) (*DecodedPoint, error)
func (d *Device) ModelByID(id uint16) *ModelInstance
```

`ReadModel` (and everything built on it) reads the model's two header
registers together with its data and compares them with the discovery result.
When the device no longer reports that ID and length at that address the read
fails with `ErrModelChanged`; the returned `DecodedModel` holds the data
registers, undecoded. A model that needs several requests is read from its
header address in chunks of 125 registers.

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
instance first, then in the enclosing ones.

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
`sunssf` point is not set at any level, the value is stored unscaled, which is
how `DecodeModel` reads such a point. Integer values without scaling are
converted exactly, including 64-bit ones beyond the precision of a `float64`.

Nothing is wrapped or clamped: a value outside the range of the point's type
after scaling fails with an error matching `ErrValueOutOfRange`.

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
defines the layout), part of a multi-register point, a model served from
`Registers`, the end marker, unmapped addresses. With `RegType`
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
exception becomes a server device failure). `OnWrite` and the function given
to `Update` run with the `Server` locked: they must not call its methods.

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

### Listening

The `Server` does not listen. Pass it to `modbus.NewServer`, which owns the
listener, connections, idle timeout, client limit, TLS, logging and metrics.
go-modbus serves `tcp://` and `tcp+tls://`. `Server.Serve(ctx, conf)` is a
convenience that starts such a server, waits for `ctx` and shuts it down
(allowing connections five seconds to end).

## Registry

```go
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

- `Device` — client + unit + discovery result
- `DiscoverOptions`, `DetectionResult`, `DiscoveryResult`, `ModelInstance`
- `DecodedModel`, `DecodedGroup`, `DecodedPoint`
- `DecodeError` — typed decode failure (see ERRORS.md)
- `Values`, `EncodeError` — values for encoding, typed encode failure
- `Server`, `ServerOptions`, `ServerModel`, `ServerWrite` — a served device
