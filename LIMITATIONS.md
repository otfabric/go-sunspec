# Limitations

What go-sunspec does not do, and what has not been verified. For the API contract see [API.md](API.md), for errors [ERRORS.md](ERRORS.md).

## Certification and verification

- **Nothing is certified.** go-sunspec has not been certified by the SunSpec Alliance or anyone else, and no conformance claim is made.
- **One independent implementation is in the test suite, on a limited set of models.** pysunspec2 covers models 1, 101, 103, 111, 120, 123, 160 and 701 ([INTEROPERABILITY.md](INTEROPERABILITY.md)). The other tests read the library's own `Server` and the [`testutil`](testutil/) fixture. Encoder and decoder share the same schemas, so a misreading of any other model definition would not show up in a round trip. No real device has been tested.
- **No device-compatibility list exists.** CI uses no hardware. Which vendors and firmware versions work is not recorded here.
- **Fuzzing is short.** CI runs each fuzz target for 10 seconds. There is no scheduled long run. `make soak` and `make fuzz FUZZTIME=...` are run by hand.
- CI runs on Linux only (`ubuntu-latest`).

## Models

- The registry holds the 112 models of a snapshot of [sunspec/models](https://github.com/sunspec/models). A weekly workflow reports upstream changes; it changes nothing by itself.
- Models without a schema, vendor models among them, are discovered and returned as raw registers: `ReadModel` adds a warning, `ReadPoint` finds no point.
- Points are decoded and scaled, not interpreted. The library does not check that values are plausible or judge what an enum or bitfield means beyond its symbol name.

## Reading

- The client does not write. It sends function codes 03 and 04 only; writing controls is left to the caller, with the go-modbus client ([README](README.md#can-go-sunspec-write-sunspec-points-controls)).
- **A model read is not atomic across requests.** Modbus allows 125 registers per request, so a longer model takes several, and a device can change between them. `ReadModel` compares the ID and length registers with discovery, not the data.
- `DecodeModel` does no such comparison: it uses the schema it is given, whatever the ID and length registers say.
- `ReadPoint` returns the first point of that name, so for a repeating group the one of the first instance.
- A point whose scale factor is missing or not implemented stays unscaled (`ScaledValue` is nil). A group that does not fit the registers, or registers left over, is a warning, not an error.
- There is no polling, subscription or caching: every read goes to the device. Timing is that of the Modbus client and the device; nothing here is real-time.
- Transport, retries, logging, metrics and connection lifecycle belong to the injected `*modbus.Client`.

## Serving

- `Server` holds registers. It models no device behaviour: an accepted write is stored as it is, and acting on it is up to `ServerOptions.OnWrite`.
- Function codes 6 and 16 write; 22 and 23 are not supported. Coils and discrete inputs do not exist. Writes must cover whole `RW` points of a model served from values.
- A client that needs several requests for a model of more than 125 registers can see different updates in them.
- Modbus has no authentication of its own: see [SECURITY.md](SECURITY.md).
