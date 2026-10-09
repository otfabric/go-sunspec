# go-sunspec — SunSpec Modbus Library and CLI for Go (Golang)

[![Go](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/otfabric/go-sunspec.svg)](https://pkg.go.dev/github.com/otfabric/go-sunspec)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI](https://github.com/otfabric/go-sunspec/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/go-sunspec/actions/workflows/ci.yml)
[![Code Coverage](https://codecov.io/gh/otfabric/go-sunspec/graph/badge.svg)](https://codecov.io/gh/otfabric/go-sunspec)
[![Release](https://img.shields.io/github/v/release/otfabric/go-sunspec?label=release)](https://github.com/otfabric/go-sunspec/releases)

**go-sunspec is a Go library and command-line tool for reading SunSpec devices over Modbus.** It discovers the SunSpec models a solar inverter, energy meter, battery or other DER device exposes, reads them over Modbus TCP, RTU or ASCII, and decodes every point into typed, scaled values with units — using the official [SunSpec Alliance](https://sunspec.org/) model definitions compiled into the binary.

Use it to build solar monitoring, energy-management (EMS), data-logging and DER-integration software in Go, or use the bundled `sunspecctl` CLI to inspect a device from the terminal. It is pure Go with no CGO, built on [otfabric/go-modbus](https://github.com/otfabric/go-modbus).

**Quick links:** [Quick start](#quick-start) · [Install](#install) · [Supported models](#supported-sunspec-models) · [Library API](#api) · [`sunspecctl` CLI](#cli--sunspecctl) · [FAQ](#faq) · [API reference](API.md)

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
- **Offline decoding** — `DecodeModel` decodes a register slice with no I/O: useful for captures, simulators and tests.
- **A CLI included** — `sunspecctl` detects, lists, reads and polls SunSpec devices, with table or JSON output.

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

### Use the CLI

```bash
# Is this a SunSpec device, and at which base address?
sunspecctl detect --url tcp://192.168.1.100:502

# Which SunSpec models does it implement?
sunspecctl models --url tcp://192.168.1.100:502

# Read and decode everything
sunspecctl read --url tcp://192.168.1.100:502

# Read AC power from the inverter model every 5 seconds, as JSON
sunspecctl poll-point --model 103 --point W --interval 5s --json --url tcp://192.168.1.100:502
```

[Install `sunspecctl`](#sunspecctl-cli) on Linux, macOS or Windows, then see [CLI — `sunspecctl`](#cli--sunspecctl) for all commands.

---

## Use cases

- **Solar PV monitoring** — Read AC/DC power, energy, voltage, current, frequency and operating state from inverters that implement SunSpec Modbus, whatever the manufacturer.
- **Energy metering** — Read SunSpec meters (models 201–204 and 211–214) for import/export energy and per-phase power.
- **Battery and storage systems** — Read state of charge, capacity and status from storage models (124, 713, 801–809).
- **Energy management systems (EMS) and DER integration** — Feed SunSpec data into controllers, dashboards, MQTT, Prometheus or time-series databases from Go services.
- **Commissioning and troubleshooting** — Check what a device really exposes with `sunspecctl` before writing integration code.
- **Testing and simulation** — Decode captured registers offline, or run the bundled fixture server in unit tests.

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
  - [Library](#library)
  - [`sunspecctl` CLI](#sunspecctl-cli) (Linux, macOS, Windows)
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
- [CLI — `sunspecctl`](#cli--sunspecctl)
- [FAQ](#faq)
- [Project Structure](#project-structure)
- [Updating Models](#updating-models)
- [Development](#development)
- [Requirements](#requirements)
- [Contributing](#contributing)
- [License](#license)

Full API and error contracts: [API.md](API.md), [ERRORS.md](ERRORS.md).

## Install

go-sunspec is two things: a Go library you import, and the `sunspecctl` command-line tool. Install whichever you need.

### Library

```bash
go get github.com/otfabric/go-sunspec
```

Requires Go 1.23 or later.

### `sunspecctl` CLI

The easiest way to install `sunspecctl` is to download a pre-built binary from the [GitHub Releases page](https://github.com/otfabric/go-sunspec/releases). No Go toolchain is required.

| Platform | CPU | Download |
|---|---|---|
| Linux | x86-64 | `sunspecctl-<version>-linux-amd64.tar.gz` |
| Linux | 64-bit ARM (Raspberry Pi 4/5 with a 64-bit OS, AWS Graviton) | `sunspecctl-<version>-linux-arm64.tar.gz` |
| Linux | 32-bit ARM (Raspberry Pi with a 32-bit OS) | `sunspecctl-<version>-linux-armv7.tar.gz` |
| macOS | Apple Silicon (M1 and later) | `sunspecctl-<version>-darwin-arm64.tar.gz` |
| macOS | Intel | `sunspecctl-<version>-darwin-amd64.tar.gz` |
| Windows | x86-64 | `sunspecctl-<version>-windows-amd64.zip` |
| Windows | ARM64 | `sunspecctl-<version>-windows-arm64.zip` |

Each release also contains `sunspecctl-<version>-checksums.txt` (SHA-256) so you can verify your download. The commands below fetch the **latest** release, verify its checksum and install it. Afterwards, check that it works:

```console
sunspecctl version
```

#### Linux

```bash
# 1. Find the latest version and your CPU architecture
VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/otfabric/go-sunspec/releases/latest | sed 's|.*/v||')
case "$(uname -m)" in
  x86_64)         ARCH=amd64 ;;
  aarch64|arm64)  ARCH=arm64 ;;
  armv7l)         ARCH=armv7 ;;
  *) echo "Unsupported architecture: $(uname -m)" ;;
esac

# 2. Download the archive and the checksum file
FILE="sunspecctl-${VERSION}-linux-${ARCH}.tar.gz"
BASE="https://github.com/otfabric/go-sunspec/releases/download/v${VERSION}"
curl -fsSLO "${BASE}/${FILE}"
curl -fsSLO "${BASE}/sunspecctl-${VERSION}-checksums.txt"

# 3. Verify the download (should print "OK")
grep "${FILE}" "sunspecctl-${VERSION}-checksums.txt" | sha256sum -c -

# 4. Extract and install (the binary in the archive carries the version and platform in its name)
mkdir -p sunspecctl-dl && tar -xzf "${FILE}" -C sunspecctl-dl
sudo install -m 0755 "$(find sunspecctl-dl -type f -name 'sunspecctl*' | head -n1)" /usr/local/bin/sunspecctl
```

No `sudo`? Install into your home directory instead (and make sure `~/.local/bin` is in your `PATH`):

```bash
mkdir -p ~/.local/bin
install -m 0755 "$(find sunspecctl-dl -type f -name 'sunspecctl*' | head -n1)" ~/.local/bin/sunspecctl
```

To talk to a device on a serial port (RS-485 adapter) without `sudo`, your user needs access to the port, usually through the `dialout` group: `sudo usermod -aG dialout "$USER"`, then log out and back in.

#### macOS

```bash
# 1. Find the latest version and your CPU architecture (arm64 = Apple Silicon, x86_64 = Intel)
VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/otfabric/go-sunspec/releases/latest | sed 's|.*/v||')
case "$(uname -m)" in
  arm64)   ARCH=arm64 ;;
  x86_64)  ARCH=amd64 ;;
esac

# 2. Download the archive and the checksum file
FILE="sunspecctl-${VERSION}-darwin-${ARCH}.tar.gz"
BASE="https://github.com/otfabric/go-sunspec/releases/download/v${VERSION}"
curl -fsSLO "${BASE}/${FILE}"
curl -fsSLO "${BASE}/sunspecctl-${VERSION}-checksums.txt"

# 3. Verify the download (should print "OK")
grep "${FILE}" "sunspecctl-${VERSION}-checksums.txt" | shasum -a 256 -c -

# 4. Extract and install (the binary in the archive carries the version and platform in its name)
mkdir -p sunspecctl-dl && tar -xzf "${FILE}" -C sunspecctl-dl
sudo install -m 0755 "$(find sunspecctl-dl -type f -name 'sunspecctl*' | head -n1)" /usr/local/bin/sunspecctl
```

If you downloaded the archive with a web browser instead of `curl` and macOS refuses to run the binary ("cannot be verified"), remove the quarantine flag: `xattr -d com.apple.quarantine /usr/local/bin/sunspecctl`.

#### Windows (PowerShell)

```powershell
# 1. Find the latest version and your CPU architecture
$version = (Invoke-RestMethod https://api.github.com/repos/otfabric/go-sunspec/releases/latest).tag_name.TrimStart('v')
$arch    = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

# 2. Download the archive and the checksum file
$file = "sunspecctl-$version-windows-$arch.zip"
$base = "https://github.com/otfabric/go-sunspec/releases/download/v$version"
Invoke-WebRequest "$base/$file" -OutFile "$env:TEMP\$file"
Invoke-WebRequest "$base/sunspecctl-$version-checksums.txt" -OutFile "$env:TEMP\sunspecctl-checksums.txt"

# 3. Verify the download (throws an error on mismatch)
$expected = ((Select-String -Path "$env:TEMP\sunspecctl-checksums.txt" -Pattern $file).Line -split '\s+')[0]
$actual   = (Get-FileHash "$env:TEMP\$file" -Algorithm SHA256).Hash
if ($expected -ne $actual) { throw "Checksum mismatch for $file" }

# 4. Extract to a per-user folder and name the program sunspecctl.exe
#    (the .exe in the archive carries the version and platform in its name)
$dir = "$env:LOCALAPPDATA\Programs\sunspecctl"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Expand-Archive "$env:TEMP\$file" -DestinationPath "$env:TEMP\sunspecctl-dl" -Force
$exe = Get-ChildItem "$env:TEMP\sunspecctl-dl" -Recurse -Filter 'sunspecctl*.exe' | Select-Object -First 1
Copy-Item $exe.FullName "$dir\sunspecctl.exe" -Force

# 5. Add sunspecctl to your user PATH (then open a new terminal)
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$dir*") {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
}
```

If Windows SmartScreen warns about an unrecognised app, choose **More info → Run anyway**.

#### Manual download

1. Open the [latest release](https://github.com/otfabric/go-sunspec/releases/latest).
2. Download the archive for your platform from the table above.
3. Extract it, rename the binary to `sunspecctl` (`sunspecctl.exe` on Windows) and move it to a folder on your `PATH`.

#### Update

Run the same commands again. They always fetch the latest release and overwrite the previous binary.

#### With Go

If you have Go 1.23 or later, you can build and install the latest release straight from source:

```bash
go install github.com/otfabric/go-sunspec/cmd/sunspecctl@latest
```

To build from a checkout, see [Building](#building).

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

Use `errors.Is` / `errors.As` with package sentinels (`ErrNotSunSpec`, `ErrUnknownModel`, `ErrPointNotFound`, `ErrDecode`, `ErrPartialRead`) and `*DecodeError`. Details: [ERRORS.md](ERRORS.md).

## CLI — `sunspecctl`

A command-line tool for inspecting SunSpec devices over Modbus.

```
sunspecctl
├── detect       Detect SunSpec device presence
├── models       List discovered SunSpec models
├── read         Read and decode all models
├── read-model   Read and decode a specific model by ID
├── read-point   Read a single named point from a model
├── poll         Repeatedly read and decode all models
├── poll-model   Repeatedly read and decode a specific model by ID
├── poll-point   Repeatedly read a single named point from a model
├── completion   Generate shell completion script (bash/zsh/fish/powershell)
└── version      Print the sunspecctl version
```

### Installing

See [Install → `sunspecctl` CLI](#sunspecctl-cli) for copy-paste commands for Linux, macOS and Windows.

### Building

```bash
# Build via Makefile (includes code generation + checks)
make build

# Or build CLI only (quick, no checks)
make build-cli

# Or build directly with go
go build -o bin/sunspecctl ./cmd/sunspecctl

# Cross-compile for all platforms (Linux amd64/arm64/armv7, macOS amd64/arm64, Windows amd64/arm64)
make build-all

# Install to /usr/local/bin
make install
```

### Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | `tcp://localhost:502` | Modbus device URL |
| `--unit-id` | `1` | Modbus unit ID |
| `--timeout` | `10s` | Operation timeout |
| `--json` | `false` | Output as JSON |
| `--raw` | `false` | Include raw register hex in output |
| `--baud` | `0` (19200) | Serial baud rate, for `rtu://` and `ascii://` URLs |
| `--data-bits` | `0` (8 for RTU, 7 for ASCII) | Serial data bits, 5–8 |
| `--parity` | `none` | Serial parity: `none`, `even` or `odd` |
| `--stop-bits` | `0` (2 without parity, 1 with) | Serial stop bits, 1 or 2 |

`--url` selects the transport: `tcp://host:port`, `rtu://<device>` (for example `rtu:///dev/ttyUSB0` or `rtu://COM3`), `ascii://<device>`, `udp://host:port`, `rtuovertcp://host:port` for a serial-to-Ethernet gateway, and the other [go-modbus URL schemes](https://github.com/otfabric/go-modbus#transport-modes). The serial flags are ignored for network URLs.

Many SunSpec devices on RS-485 use 9600 baud, 8 data bits, no parity and 1 stop bit, which is not the default: pass `--baud 9600 --stop-bits 1` (and the device's Modbus address as `--unit-id`).

### Commands

```bash
# Detect SunSpec presence
sunspecctl detect --url tcp://192.168.1.100:502

# List discovered models
sunspecctl models --url tcp://192.168.1.100:502

# Read and decode all models
sunspecctl read --url tcp://192.168.1.100:502

# Read a specific model by ID
sunspecctl read-model --id 101 --url tcp://192.168.1.100:502

# Read a single point from a model
sunspecctl read-point --model 101 --point W --url tcp://192.168.1.100:502

# SunSpec over Modbus RTU (RS-485) at 9600 8N1, device address 3
sunspecctl models --url rtu:///dev/ttyUSB0 --baud 9600 --stop-bits 1 --unit-id 3

# A serial device behind an RS-485-to-Ethernet gateway
sunspecctl read --url rtuovertcp://192.168.1.50:4001 --unit-id 3
```

### Polling

The `poll`, `poll-model`, and `poll-point` commands work like their `read` counterparts but repeat at a configurable interval.

| Flag | Default | Description |
|------|---------|-------------|
| `--interval` | `30s` | Interval between polls (supports `s`, `m`, `h`) |
| `--count` | `0` | Number of polls (`0` = infinite, stop with Ctrl-C) |

```bash
# Poll all models every 10 seconds
sunspecctl poll --interval 10s --url tcp://192.168.1.100:502

# Poll model 101 five times, once per minute
sunspecctl poll-model --id 101 --interval 1m --count 5 --url tcp://192.168.1.100:502

# Poll a single point every 5 seconds as JSON (great for piping)
sunspecctl poll-point --model 101 --point W --interval 5s --json --url tcp://192.168.1.100:502
```

### Output Formats

```bash
# Default: human-readable table
sunspecctl models --url tcp://192.168.1.100:502

# JSON output (structured, suitable for piping to jq)
sunspecctl models --url tcp://192.168.1.100:502 --json

# Include raw register hex alongside decoded values
sunspecctl read --url tcp://192.168.1.100:502 --raw
```

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

Not through this package: it reads and decodes. You can write registers with the same go-modbus client (`client.WriteRegisters`) using the address and offset from the discovered model and its schema.

### How do scale factors work?

Many SunSpec integer points are paired with a scale factor point (type `sunssf`) holding a power-of-ten exponent. go-sunspec resolves the reference and fills `ScaledValue`, so a raw `W` of `1234` with `W_SF` of `-1` becomes `123.4`. The raw value and the scale factor are kept as well.

### How are the model definitions kept up to date?

They are generated from the official [sunspec/models](https://github.com/sunspec/models) JSON files. A weekly GitHub Actions workflow compares the local copy with upstream and opens an issue when models were added or changed. See [Updating models](#updating-models).

## Project Structure

```
go-sunspec/
├── cmd/sunspecctl/     CLI tool
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
└── read.go            Batch register reader (>125 splitting)
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
```

Tests need no hardware: [`testutil`](testutil/) provides a SunSpec fixture
server. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Requirements

- Go 1.23+
- [otfabric/go-modbus](https://github.com/otfabric/go-modbus) v1.2.1+

## Contributing

Bug reports, device-compatibility notes and pull requests are welcome. Please [open an issue](https://github.com/otfabric/go-sunspec/issues) to discuss larger changes first, and run `make check` before submitting. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup and guidelines.

Please report security vulnerabilities privately, as described in [SECURITY.md](SECURITY.md), not in a public issue.

If go-sunspec is useful to you, a star on GitHub helps other Go developers find it.

## License

This project is licensed under the MIT License. See [LICENSE](./LICENSE).

The SunSpec JSON model definitions in [`models/`](models/) are from
[sunspec/models](https://github.com/sunspec/models) and are licensed under the
[Apache License 2.0](https://github.com/sunspec/models/blob/master/LICENSE).
