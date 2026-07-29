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

## Registry

```go
func registry.ByID(id uint16) *ModelMeta
func registry.Known(id uint16) bool
func registry.Count() int
func registry.All() map[uint16]*ModelMeta // shallow copy of the map
func registry.Register(m *ModelMeta)      // for generated init only
```

## Core types

- `Device` — client + unit + discovery result
- `DiscoverOptions`, `DetectionResult`, `DiscoveryResult`, `ModelInstance`
- `DecodedModel`, `DecodedBlock`, `DecodedPoint`
- `DecodeError` — typed decode failure (see ERRORS.md)
