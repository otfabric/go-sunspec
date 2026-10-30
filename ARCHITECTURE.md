# Architecture

go-sunspec reads and serves SunSpec devices over Modbus. It does not implement Modbus: every transport concern belongs to go-modbus. File by file: [README](README.md#project-structure).

## Layers

Matches the import graph:

```
devicefile ──────┐        sunspec (root)
                 │          │    detect, discover, decode, encode, device reads, Server
                 ▼          ▼
              registry   go-modbus, go-modbus/batch, go-modbus/sunspec
```

`registry` is generated from `models/` by `internal/gen`, which uses `internal/schema`, and imports nothing from this module. Both `internal` packages are generator-only: neither the root package nor `registry` imports them. `testutil` is a fixture server on go-modbus.

## Ownership and goroutines

The caller creates and closes the `*modbus.Client`; see [API.md](API.md#ownership-and-lifecycle). `Device` methods are safe for concurrent use to the same extent as that client. `Server` is a go-modbus `RequestHandler`: it runs on go-modbus's server goroutines and starts none of its own for serving. An update or accepted write replaces a model's registers atomically under the `Server`'s lock, and `ServerOptions.OnWrite` runs under that lock.

## Reads

A model longer than 125 registers takes several requests, so a read is not atomic across them ([LIMITATIONS.md](LIMITATIONS.md#reading)). Every read goes to the device: there is no cache.

## Platforms

No platform-specific code in this module. CI runs on Linux only.
