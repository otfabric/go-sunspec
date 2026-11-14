# Architecture

go-sunspec reads, writes and serves SunSpec devices over Modbus. It does not implement Modbus: every transport concern belongs to go-modbus. File by file: [README](README.md#project-structure).

## Layers

Matches the import graph:

```
devicefile ──────┐        sunspec (root)
                 │          │    detect, discover, decode, encode, device reads, WritePoint, Server
                 ▼          ▼
              registry   go-modbus, go-modbus/batch, go-modbus/sunspec
```

`registry` is generated from `models/` by `internal/gen`, which uses `internal/schema`, and imports nothing from this module. Both `internal` packages are generator-only: neither the root package nor `registry` imports them. `testutil` is a fixture server on go-modbus.

## Ownership and goroutines

The caller creates and closes the `*modbus.Client`; see [API.md](API.md#ownership-and-lifecycle). `Device` methods are safe for concurrent use to the same extent as that client. `Server` is a go-modbus `RequestHandler`: it runs on go-modbus's server goroutines and starts none of its own for serving. An update or accepted write replaces a model's registers atomically under the `Server`'s lock.

No callback runs under that lock (CONC-R02; since v0.11.0). Each served model has a generation counter that every update and every accepted write moves on. `ServerOptions.OnWrite` and the function given to `Update` work on a copy taken at one generation, outside the lock, and the result is stored under the lock only if the generation is still the same. Otherwise a client write is refused with exception 4, since the model `OnWrite` judged is not the model any more, and `Update` runs its function again on the new values, since an update must not be lost. A `Server` without `OnWrite` applies a write in one locked step. The contract of both callbacks (goroutine, blocking, re-entry, what does not return normally) is in [API.md](API.md#the-onwrite-contract).

## Reads

A model longer than 125 registers takes several requests, so a read is not atomic across them ([LIMITATIONS.md](LIMITATIONS.md#reading)). Every read goes to the device: there is no cache. `Device.WritePoint` reads the model, writes one point and reads the model again: separate requests, not a transaction, so a device that changes the point's scale factor in between gets the value scaled with the old one.

## Platforms

No platform-specific code in this module. CI runs on Linux only.
