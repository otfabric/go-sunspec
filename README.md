# go-sunspec — SunSpec Modbus Protocol Library

[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE.txt)
[![Go Report Card](https://goreportcard.com/badge/github.com/otfabric/go-sunspec)](https://goreportcard.com/report/github.com/otfabric/go-sunspec)
[![CI](https://github.com/otfabric/go-sunspec/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/go-sunspec/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/otfabric/go-sunspec?display_name=tag)](https://github.com/otfabric/go-sunspec/releases)


Go library for reading [SunSpec](https://sunspec.org/) devices over Modbus. Built on top of [otfabric/go-modbus](https://github.com/otfabric/go-modbus).

- Auto-discovers SunSpec models on a device
- Decodes all standard point types (int, uint, float, string, enum, bitfield, IP addresses, accumulators, scale factors)
- Resolves scale factors automatically (both point-reference and literal)
- Handles repeating blocks (meters, MPPTs, etc.)
- Ships with a compiled registry of 112 SunSpec model schemas
- Includes `sunspecctl` CLI for quick device inspection

## Install

```bash
go get github.com/otfabric/go-sunspec
```

## Quick Start

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
    client, _ := modbus.New(modbus.Config{
        URL: "tcp://192.168.1.100:502",
    })
    client.Open()
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
        if dm.FixedBlock != nil {
            for _, p := range dm.FixedBlock.Points {
                if !p.Implemented {
                    continue
                }
                if p.ScaledValue != nil {
                    fmt.Printf("  %s = %g %s\n", p.Name, *p.ScaledValue, p.Units)
                } else {
                    fmt.Printf("  %s = %v\n", p.Name, p.RawValue)
                }
            }
        }
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

### Registry

```go
import "github.com/otfabric/go-sunspec/registry"

// Look up model metadata.
meta := registry.ByID(101) // *ModelMeta or nil
known := registry.Known(101) // true
count := registry.Count() // 112
all := registry.All() // sorted []ModelMeta
```

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

### Building

```bash
# Build via Makefile (includes code generation + checks)
make build

# Or build CLI only (quick, no checks)
make build-cli

# Or build directly with go
go build -o bin/sunspecctl ./cmd/sunspecctl

# Cross-compile for all platforms (linux/amd64, linux/arm64, linux/armv7, darwin/amd64, darwin/arm64)
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

## Project Structure

```
go-sunspec/
├── cmd/sunspecctl/     CLI tool
├── internal/
│   ├── gen/           Code generator (JSON → Go)
│   └── schema/        JSON model parsing types
├── models/            SunSpec JSON model definitions
├── registry/          Generated model metadata + lookups
├── testutil/          Test fixture server
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

Sync the latest SunSpec JSON models and regenerate:

```bash
./sync-models.sh
make generate
```

## Requirements

- Go 1.23+
- [otfabric/go-modbus](https://github.com/otfabric/go-modbus) v1.0.4+
