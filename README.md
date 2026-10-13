# go-sunspec — SunSpec Modbus Library for Go (Golang)

[![Go](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/otfabric/go-sunspec.svg)](https://pkg.go.dev/github.com/otfabric/go-sunspec)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI](https://github.com/otfabric/go-sunspec/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/go-sunspec/actions/workflows/ci.yml)
[![Code Coverage](https://codecov.io/gh/otfabric/go-sunspec/graph/badge.svg)](https://codecov.io/gh/otfabric/go-sunspec)
[![Release](https://img.shields.io/github/v/release/otfabric/go-sunspec?label=release)](https://github.com/otfabric/go-sunspec/releases)

**go-sunspec is a Go library for reading and serving SunSpec devices over Modbus.** It discovers the SunSpec models a solar inverter, energy meter, battery or other DER device exposes, reads them over Modbus TCP, RTU or ASCII, and decodes every point into typed, scaled values with units — using the official [SunSpec Alliance](https://sunspec.org/) model definitions compiled into your program. The same definitions work the other way: build a device from models and point values and [serve it](#serving-a-device) over Modbus TCP, as a simulator or as the SunSpec interface of your own equipment.

Use it to build solar monitoring, energy-management (EMS), data-logging, DER-integration and device-simulation software in Go, or use [`sunspecctl`](https://github.com/otfabric/sunspecctl), the command-line tool built on it, to inspect or simulate a device from the terminal. It is pure Go with no CGO, built on [otfabric/go-modbus](https://github.com/otfabric/go-modbus).

**Quick links:** [Quick start](#quick-start) · [Install](#install) · [Supported models](#supported-sunspec-models) · [Library API](#api) · [Serving a device](#serving-a-device) · [`sunspecctl` CLI](#command-line-tool) · [FAQ](#faq) · [API reference](API.md)

---

## Why go-sunspec?

- **Automatic discovery** — Finds the `SunS` marker at the common base addresses (0, 40000, 50000 and their off-by-one neighbours, or ones you supply), walks the model chain and tells you which models the device implements.
- **Typed, scaled values** — Decodes every SunSpec point type (`int16`–`uint64`, `acc16`–`acc64`, `float32`/`float64`, `string`, `enum16`/`enum32`, `bitfield16`–`bitfield64`, `ipaddr`, `ipv6addr`, `eui48`, `sunssf`) and applies scale factors for you, so `W` is watts, not a raw register and an exponent.
- **112 models built in** — Inverters (101–103, 111–113), meters (201–204, 211–214), nameplate, settings and controls (120–145), multiple-MPPT (160), DER models (701–715), batteries and storage (801–809), environmental sensors (302–308), string combiners (401–404) and more, generated from the official [sunspec/models](https://github.com/sunspec/models) JSON.
- **Unimplemented points are flagged** — SunSpec "not implemented" sentinel values are detected per type, so a missing measurement is reported as missing, not as `65535`.
- **Nested and repeating groups** — Meter phases, MPPT trackers and battery strings are decoded as repeated group instances, and models that nest groups (DER curves with their points, trip settings three levels deep) come back as the same tree the model defines.
- **Enums and bitfields by name** — Operating states, events and alarms are returned with their symbol names.
- **Unknown and vendor models survive** — A model without a local schema is still discovered and returned with its raw registers.
- **Every Modbus transport** — Modbus TCP, serial RTU and ASCII (RS-485), TLS, UDP and serial-to-Ethernet bridges, with retries, timeouts, logging and metrics configured on the go-modbus client.
- **Offline decoding and encoding** — `DecodeModel` decodes a register slice with no I/O, and `EncodeModel` builds one from point values: useful for captures, simulators and tests.
- **Serve a device** — `Server` lays out a model chain from values given by point name, serves it through go-modbus, accepts client writes to writable points and lets you change values while it runs.
- **A CLI built on it** — [`sunspecctl`](https://github.com/otfabric/sunspecctl) detects, lists, reads and polls SunSpec devices, with table or JSON output, and serves simulated devices from a file.

---

## Quick start

### Use the library

```bash
go get github.com/otfabric/go-sunspec
```

```go
client, _ := modbus.New(modbus.Config{URL: "tcp://192.168.1.100:502"})
if err := client.Open(); err != nil {
    log.Fatal(err)
}
defer client.Close()

device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{UnitID: 1})
if err != nil {
    log.Fatal(err) // errors.Is(err, sunspec.ErrNotSunSpec) when the device is not SunSpec
}

inverter, err := device.ReadModelByID(ctx, 103) // three-phase inverter
if err != nil {
    log.Fatal(err)
}
for _, p := range inverter.Group.Points {
    if p.Implemented && p.ScaledValue != nil {
        fmt.Printf("%s = %g %s\n", p.Name, *p.ScaledValue, p.Units)
    }
}
```

A complete program is in [Example](#example).

### From the command line

[`sunspecctl`](https://github.com/otfabric/sunspecctl) is the command-line tool built on this library, with pre-built binaries for Linux, macOS and Windows:

```bash
sunspecctl detect --url tcp://192.168.1.100:502    # is this a SunSpec device?
sunspecctl models --url tcp://192.168.1.100:502    # which models does it implement?
sunspecctl read   --url tcp://192.168.1.100:502    # read and decode everything
```

---

## Use cases

- **Solar PV monitoring** — Read AC/DC power, energy, voltage, current, frequency and operating state from inverters that implement SunSpec Modbus, whatever the manufacturer.
- **Energy metering** — Read SunSpec meters (models 201–204 and 211–214) for import/export energy and per-phase power.
- **Battery and storage systems** — Read state of charge, capacity and status from storage models (124, 713, 801–809).
- **Energy management systems (EMS) and DER integration** — Feed SunSpec data into controllers, dashboards, MQTT, Prometheus or time-series databases from Go services.
- **Commissioning and troubleshooting** — Check what a device really exposes with [`sunspecctl`](https://github.com/otfabric/sunspecctl) before writing integration code.
- **Testing and simulation** — Serve a simulated inverter, meter or battery to develop and test an EMS or a SCADA integration without hardware; decode captured registers offline.
- **SunSpec interface for your own device** — Expose measurements and settings of a Go-controlled device as standard SunSpec models.

---

## Supported SunSpec models

The registry contains the 112 models published in the official [sunspec/models](https://github.com/sunspec/models) repository:

| Model IDs | Area |
|---|---|
| 1 | Common (manufacturer, model, serial number, version) |
| 2–19 | Aggregator, secure dataset and network interface models |
| 101–103, 111–113 | Inverters: single, split and three phase (integer and float) |
| 120–145 | Nameplate, settings, measurements and status, immediate controls, storage, pricing, volt-VAR, frequency-watt, ride-through curves, scheduling |
| 160 | Multiple MPPT inverter extension |
| 201–204, 211–214, 220 | Meters: single, split, wye and delta (integer and float), secure meter readings |
| 302–308 | Irradiance, module temperature, inclinometer, GPS, meteorological |
| 401–404 | String combiners |
| 501–502, 601 | Solar module, tracker controller |
| 701–715 | DER (IEEE 1547-2018): AC and DC measurement, capacity, enter service, controls, volt-var, volt-watt, trip settings, frequency droop, watt-var, storage capacity |
| 801–809 | Energy storage: battery base, lithium-ion bank/string/module, flow battery |
| 63001–64415 | SunSpec test models and vendor models |

Models that are not in the registry (vendor-specific ones, for example) are still discovered; they are returned with raw registers and a warning. A [weekly check](.github/workflows/models-check.yml) watches the upstream repository for new and changed models; see [Updating models](#updating-models).

```go
registry.Known(103)      // true
registry.ByID(103).Label // "Inverter (Three Phase)"
registry.Count()         // 112
```

---

## Table of Contents

- [Why go-sunspec?](#why-go-sunspec)
- [Quick start](#quick-start)
- [Use cases](#use-cases)
- [Supported SunSpec models](#supported-sunspec-models)
- [Install](#install)
- [Example](#example)
- [API](#api)
  - [Discovery](#discovery)
  - [Reading](#reading)
  - [Decoded values](#decoded-values)
  - [Serial (RTU) and other transports](#serial-rtu-and-other-transports)
  - [Registry](#registry)
  - [Ownership and concurrency](#ownership-and-concurrency)
  - [Observability](#observability)
  - [Errors](#errors)
- [Serving a device](#serving-a-device)
- [Command-line tool](#command-line-tool)
- [FAQ](#faq)
- [Project Structure](#project-structure)
- [Updating Models](#updating-models)
- [Development](#development)
- [Requirements](#requirements)
- [Contributing](#contributing)
- [License](#license)

Full API and error contracts: [API.md](API.md), [ERRORS.md](ERRORS.md).

## Install

```bash
go get github.com/otfabric/go-sunspec
```

Requires Go 1.23 or later.

Looking for the `sunspecctl` command-line tool? It has its own repository, with install instructions for Linux, macOS and Windows: [otfabric/sunspecctl](https://github.com/otfabric/sunspecctl#install).

## Example

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/otfabric/go-modbus"
    "github.com/otfabric/go-sunspec"
)

func main() {
    client, err := modbus.New(modbus.Config{
        URL: "tcp://192.168.1.100:502",
    })
    if err != nil {
        log.Fatal(err)
    }
    if err := client.Open(); err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    ctx := context.Background()

    device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{UnitID: 1})
    if err != nil {
        log.Fatal(err)
    }

    results, err := device.ReadAll(ctx)
    if err != nil {
        log.Fatal(err)
    }

    for _, dm := range results {
        fmt.Printf("Model %d (%s)\n", dm.ModelID, dm.Name)
        // Walk visits the model's top-level group and every nested group instance.
        dm.Group.Walk(func(g *sunspec.DecodedGroup) {
            for _, p := range g.Points {
                if !p.Implemented {
                    continue
                }
                if p.ScaledValue != nil {
                    fmt.Printf("  %s = %g %s\n", p.Name, *p.ScaledValue, p.Units)
                } else {
                    fmt.Printf("  %s = %v\n", p.Name, p.RawValue)
                }
            }
        })
    }
}
```

## API

### Discovery

```go
// Detect checks if a device speaks SunSpec.
result, err := sunspec.Detect(ctx, client, opts)

// Discover enumerates all models and returns a Device ready for reading.
device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{
    UnitID:        1,
    BaseAddresses: []uint16{40000, 50000, 0},
})
```

### Reading

```go
// Read all models at once.
decoded, err := device.ReadAll(ctx)

// Read a specific model by ID.
dm, err := device.ReadModelByID(ctx, 101)

// Read a single point.
inst := device.ModelByID(101)
point, err := device.ReadPoint(ctx, *inst, "W")
fmt.Printf("Power: %g W\n", *point.ScaledValue)
```

Each read of a model includes its ID and length registers and checks them against the discovery result: a device whose model chain changed since `Discover` is reported with `ErrModelChanged` instead of being decoded with a stale layout.

### Decoded values

Every point of a decoded model is a `DecodedPoint`:

| Field | Meaning |
|---|---|
| `Name`, `Type`, `Units` | Point name, SunSpec type and units from the model definition |
| `Implemented` | `false` when the device reports the type's "not implemented" value |
| `RawValue` | The value as read: an integer, float, string, IP or MAC address |
| `ScaledValue` | `RawValue × 10^scale factor` as `*float64`; `nil` for non-numeric points and when no scale factor applies |
| `SFName`, `SFRawValue` | The scale factor point (or literal) and its value |
| `Symbols` | Names of the active enum value or bitfield bits |
| `RegisterOffset`, `RegisterCount` | Where the point sits in the model, counted from its ID register |

#### Groups

A SunSpec model is a tree of groups, and a decoded model has the same shape. `DecodedModel.Group` is the top-level group with the model's own points; `Group.Groups` holds the instances of the groups nested inside it, in register order, each with its own `Points` and `Groups`:

| Model | `Group.Points` | `Group.Groups` |
|---|---|---|
| 103 inverter | all points | none |
| 160 multiple MPPT | `DCA_SF`, `N`, … | one `module` instance per tracker |
| 213 meter | all points | none |
| 705 DER volt-var | `Ena`, `NCrv`, `NPt`, `V_SF`, … | one `Crv` instance per curve, each holding one `Pt` instance per curve point |
| 707 DER trip LV | `Ena`, `NCrvSet`, `NPt`, … | `Crv` → `MustTrip`, `MayTrip`, `MomCess` → `Pt` |

```go
dm, _ := device.ReadModelByID(ctx, 705)
for _, crv := range dm.Group.GroupsNamed("Crv") {          // crv.Index is 1, 2, …
    for _, pt := range crv.GroupsNamed("Pt") {
        fmt.Println(crv.Index, pt.Index, *pt.Point("V").ScaledValue, *pt.Point("Var").ScaledValue)
    }
}

w := dm.Point("W") // first point with that name anywhere in the model, or nil
```

`Point` looks at a group's own points, `FindPoint` also searches the groups inside it, `GroupsNamed` returns the nested instances of one group and `Walk` visits the whole tree. How many instances a nested group has comes from the model: a fixed number, the value of a count point such as `NCrv`, or as many as the model's length holds. Scale factors are resolved through the tree, so a curve point scaled by `V_SF` finds it in the top-level group. `Warnings` lists anything that could not be decoded.

### Serial (RTU) and other transports

go-sunspec reads through a [go-modbus](https://github.com/otfabric/go-modbus) client, so every transport that library supports works unchanged — only the client configuration differs:

```go
// SunSpec over Modbus RTU on RS-485
client, err := modbus.New(modbus.Config{
    URL:      "rtu:///dev/ttyUSB0",
    Speed:    9600,
    DataBits: 8,
    Parity:   modbus.ParityNone,
    StopBits: 1,
})

// A serial device behind an RS-485-to-Ethernet gateway
client, err := modbus.New(modbus.Config{URL: "rtuovertcp://192.168.1.50:4001"})
```

Pass the device's Modbus address as `DiscoverOptions.UnitID`.

### Registry

```go
import "github.com/otfabric/go-sunspec/registry"

// Look up model metadata.
meta := registry.ByID(101) // *ModelMeta or nil
known := registry.Known(101) // true
count := registry.Count() // 112
all := registry.All() // map[uint16]*ModelMeta (shallow copy)
```

### Ownership and concurrency

- The caller owns the `*modbus.Client`: create, `Open`, and `Close` it. `Device` only holds a reference and does not close the connection.
- `sunspec.Open` is an alias of `Discover`; it does **not** open the Modbus connection.
- Concurrent `Device` use follows the injected client's concurrency rules (see go-modbus).
- The compiled registry is safe for concurrent reads after init; do not call `registry.Register` afterward.

### Observability

Logging, metrics, retries, and timeouts are configured on the Modbus client, not on this package:

```go
client, err := modbus.New(modbus.Config{
    URL:    "tcp://192.168.1.100:502",
    Logger: modbus.NopLogger(),
})
```

### Errors

Use `errors.Is` / `errors.As` with package sentinels (`ErrNotSunSpec`, `ErrUnknownModel`, `ErrPointNotFound`, `ErrDecode`, `ErrPartialRead`, `ErrModelChanged`, and on the serving side `ErrEncode`, `ErrValueOutOfRange`, `ErrModelLayout`) and `*DecodeError` / `*EncodeError`. Details: [ERRORS.md](ERRORS.md).

## Serving a device

A `Server` is a SunSpec device held in memory: the `SunS` marker, each model with its ID and length registers and its data, and the end marker. You give the models as point values by SunSpec name; the library encodes them with the registry's schemas.

```go
srv, err := sunspec.NewServer(&sunspec.ServerOptions{BaseAddress: 40000, UnitID: 1},
    sunspec.ServerModel{ID: 1, Values: (&sunspec.Values{}).
        Set("Mn", "Acme").Set("Md", "PV-5000").Set("SN", "A-0001")},
    sunspec.ServerModel{ID: 103, Values: (&sunspec.Values{}).
        Set("W", 4200).Set("W_SF", 0).        // 4200 W
        Set("PhVphA", 230.4).Set("V_SF", -1). // stored as 2304
        Set("St", "MPPT")},                   // enum by symbol name
    sunspec.ServerModel{ID: 64900, Registers: []uint16{1, 2, 3}}, // vendor model, raw
)
if err != nil {
    return err
}

// A Server is a go-modbus RequestHandler: the Modbus server stays yours.
ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://0.0.0.0:1502"}, srv)
if err != nil {
    return err
}
if err := ms.Start(); err != nil {
    return err
}
defer ms.Stop()

// Change values while it is being read.
err = srv.SetPoint(1, "W", 4350) // model at index 1 of the chain
```

`srv.Serve(ctx, &modbus.ServerConfig{...})` does the `NewServer` / `Start` / wait / `Shutdown` sequence in one call when you do not need the Modbus server yourself.

- **Values** — A point takes its engineering value; the encoder divides by 10^scale factor and rounds to the nearest integer, halves away from zero. The scale factor is the value you give for the `sunssf` point. A point you do not set is served as "not implemented". A value that does not fit its type after scaling is an error (`ErrValueOutOfRange`), never wrapped or clamped.
- **Groups** — `Values.Add("module", ...)` adds an instance of a repeating group; count points such as `NPt` are filled in from the number of instances. Nested groups nest.
- **Updates** — `SetPoint`, `SetValues`, `Update` (several points, nested ones, in one atomic step) and `SetRegisters` are safe while the device is served. A read of a model in a single Modbus request never sees half an update. The layout is fixed: an update cannot change a model's length.
- **Client writes** — Clients can write (FC 6 and 16) points whose SunSpec access is `RW`. Read-only points, headers, the marker, count points, parts of a point and unmapped addresses are refused with exception 2. `ServerOptions.OnWrite` sees every write, decoded, before it takes effect and can refuse it.
- **Without I/O** — `EncodeModel(schema, values)` returns the registers of one model, and `DecodedModel.Values()` turns a decoded model back into values: `EncodeModel` and `DecodeModel` are each other's inverse.

Full contract: [API.md](API.md#serving). There is no authentication in Modbus: see [SECURITY.md](SECURITY.md) before you listen on a network.

## Command-line tool

`sunspecctl`, the command-line tool for inspecting SunSpec devices, is built on this library and lives in its own repository: **[otfabric/sunspecctl](https://github.com/otfabric/sunspecctl)**. It detects SunSpec devices, lists their models, and reads or polls models and single points as text or JSON, over the same transports as the library.

It also serves simulated devices from a file (`sunspecctl serve`), using the [`Server`](#serving-a-device) of this library.

Up to go-sunspec v0.4.2 it was part of this repository (`cmd/sunspecctl`) and its binaries were attached to the go-sunspec releases. From v0.5.0 on, releases of this repository are the library only; `sunspecctl` binaries and `go install github.com/otfabric/sunspecctl@latest` come from the new repository. Its version numbering starts again at v0.1.0 there.

## FAQ

### What is SunSpec?

[SunSpec](https://sunspec.org/) is an open standard from the SunSpec Alliance that defines how solar inverters, meters, batteries and other distributed energy resources (DER) expose their data over Modbus. A device publishes a chain of numbered *models* (blocks of registers with a defined layout) starting at a well-known address marked with the ASCII characters `SunS`. Because the layout is standardized, the same code reads devices from different manufacturers.

### How do I read a SunSpec inverter in Go?

Create a go-modbus client, call `sunspec.Discover`, then read a model: `device.ReadModelByID(ctx, 103)` for a three-phase inverter (101 single phase, 102 split phase; 111–113 are the float variants). Each point comes back with its scaled value and units. See [Quick start](#quick-start).

### Which devices does go-sunspec work with?

Any device that implements SunSpec Modbus: inverters, meters, batteries and DER controllers from vendors that follow the standard. Run `sunspecctl detect` and `sunspecctl models` against a device to see whether it is SunSpec-enabled and which models it exposes. Some devices need SunSpec or Modbus enabled in their settings first.

### Does it work over Modbus RTU / RS-485?

Yes. go-sunspec reads through go-modbus, which supports Modbus TCP, serial RTU and ASCII, TLS, UDP and RTU or ASCII over TCP. In code, see [Serial (RTU) and other transports](#serial-rtu-and-other-transports); with the CLI, use `--url rtu:///dev/ttyUSB0` and the `--baud`, `--data-bits`, `--parity` and `--stop-bits` flags.

### Where does SunSpec data start? My device is not detected.

Detection probes the common base addresses (0, 40000 and 50000, and their off-by-one neighbours) in holding registers. If your device uses another address or input registers, set `DiscoverOptions.BaseAddresses` and `RegType`. Also check the Modbus unit ID (`UnitID`): many inverters do not answer on unit 1. `sunspec.Detect` returns every probe attempt and its error, which shows what the device answered.

### What happens with models the library does not know?

They are still discovered and listed. `ReadModel` returns them with raw registers and a warning, and `ModelInstance.SchemaKnown` is `false`.

### Can go-sunspec write SunSpec points (controls)?

Not as a client: `Device` reads and decodes. You can write registers with the same go-modbus client (`client.WriteRegisters`) using the address and offset from the discovered model and its schema; `EncodeModel` gives you the registers for values by point name. On the serving side it is the other way round: a [`Server`](#serving-a-device) accepts writes from clients to the points SunSpec defines as writable.

### Can I simulate a SunSpec device?

Yes. Build a [`Server`](#serving-a-device) from models and point values and serve it with go-modbus, or without writing code run `sunspecctl serve` with a device file; `sunspecctl capture` writes such a file from a real device.

### My device changed its models while my program was running. What happens?

Every read of a model also reads its ID and length registers and compares them with what discovery found. If they differ, the read fails with `ErrModelChanged` instead of decoding the registers with a layout that no longer applies. Call `Discover` again.

### How do scale factors work?

Many SunSpec integer points are paired with a scale factor point (type `sunssf`) holding a power-of-ten exponent. go-sunspec resolves the reference and fills `ScaledValue`, so a raw `W` of `1234` with `W_SF` of `-1` becomes `123.4`. The raw value and the scale factor are kept as well.

### How are the model definitions kept up to date?

They are generated from the official [sunspec/models](https://github.com/sunspec/models) JSON files. A weekly GitHub Actions workflow compares the local copy with upstream and opens an issue when models were added or changed. See [Updating models](#updating-models).

## Project Structure

```
go-sunspec/
├── internal/
│   ├── gen/           Code generator (JSON → Go)
│   └── schema/        JSON model parsing types
├── models/            SunSpec JSON models (from sunspec/models; see models/README.md)
├── registry/          Generated model metadata + lookups
├── testutil/          Test fixture server
├── sync-models.sh     Download the SunSpec JSON models from sunspec/models
├── check-models.sh    Report models that are new, changed or removed upstream
├── API.md             Public API contract
├── ERRORS.md          Error taxonomy
├── doc.go             Package documentation
├── types.go           Public types (Device, ModelInstance, DiscoverOptions)
├── errors.go          Error types
├── detect.go          Detect()
├── discover.go        Discover(), Open()
├── decode.go          DecodeModel()
├── decode_point.go    Per-type point decoders
├── decode_sf.go       Scale factor resolution
├── device.go          ReadAll, ReadModel, ReadModelByID, ReadPoint
├── read.go            Batch register reader (>125 splitting)
├── encode.go          Values, EncodeModel(), DecodedModel.Values()
├── encode_point.go    Per-type point encoders
└── server.go          Server: a served device (go-modbus RequestHandler)
```

## Updating Models

The JSON under [`models/`](models/) comes from the official SunSpec Alliance
repository [**sunspec/models**](https://github.com/sunspec/models)
([`json/` on `master`](https://github.com/sunspec/models/tree/master/json)).
[`sync-models.sh`](sync-models.sh) downloads that tree into `models/`;
`make generate` compiles it into [`registry/models_gen.go`](registry/models_gen.go).
See [models/README.md](models/README.md) for details.

```bash
./check-models.sh         # report what is new, changed or removed upstream (read-only)
./check-models.sh --diff  # also show which points changed and the JSON diff
./sync-models.sh   # fetch latest JSON from sunspec/models
make generate      # regenerate registry/models_gen.go
```

`make models-check` and `make sync` wrap the same steps. The
[SunSpec models check](.github/workflows/models-check.yml) workflow runs
`check-models.sh` every week and opens an issue (or updates the open one) when
upstream has new, changed or removed models. The issue shows what changed —
the points that differ and the JSON diff between the local copy and upstream —
and closes itself once `models/` is back in sync.

## Development

```bash
make check         # format, vet, lint, vulnerability scan, race tests, coverage gate
make test          # tests with the race detector
make coverage      # coverage report for the library packages
make soak          # chaos and stress tests for SOAKTIME each (default 2m)
```

Tests need no hardware: the library's own `Server` and the [`testutil`](testutil/)
fixture stand in for devices. Next to the unit tests there are end-to-end tests
(served device against the client path over TCP), chaos tests (faults on the
wire and misbehaving devices) and parallel stress tests. See
[CONTRIBUTING.md](CONTRIBUTING.md).

## Requirements

- Go 1.23+
- [otfabric/go-modbus](https://github.com/otfabric/go-modbus) v1.4.0+

## Contributing

Bug reports, device-compatibility notes and pull requests are welcome. Please [open an issue](https://github.com/otfabric/go-sunspec/issues) to discuss larger changes first, and run `make check` before submitting. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup and guidelines.

Please report security vulnerabilities privately, as described in [SECURITY.md](SECURITY.md), not in a public issue.

If go-sunspec is useful to you, a star on GitHub helps other Go developers find it.

## License

This project is licensed under the MIT License. See [LICENSE](./LICENSE).

The SunSpec JSON model definitions in [`models/`](models/) are from
[sunspec/models](https://github.com/sunspec/models) and are licensed under the
[Apache License 2.0](https://github.com/sunspec/models/blob/master/LICENSE).
