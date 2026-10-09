# go-sunspec API reference

Public API of `github.com/otfabric/go-sunspec` (package `sunspec`) and
`github.com/otfabric/go-sunspec/registry`.

Errors: [ERRORS.md](ERRORS.md). Release notes: [RELEASE.md](RELEASE.md).

## Ownership and lifecycle

| Object | Owner | Notes |
|--------|-------|-------|
| `*modbus.Client` | Caller | Create with `modbus.New`, open/close explicitly |
| `*sunspec.Device` | Caller | Holds a reference to the client; does **not** close it |
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

## Device reads

```go
func (d *Device) ReadAll(ctx context.Context) ([]*DecodedModel, error)
func (d *Device) ReadModel(ctx context.Context, inst ModelInstance) (*DecodedModel, error)
func (d *Device) ReadModelByID(ctx context.Context, modelID uint16) (*DecodedModel, error)
func (d *Device) ReadPoint(ctx context.Context, inst ModelInstance, pointName string) (*DecodedPoint, error)
func (d *Device) ModelByID(id uint16) *ModelInstance
```

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
