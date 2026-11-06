# Limitations

What go-sunspec does not do, and what has not been verified. For the API contract see [API.md](API.md), for errors [ERRORS.md](ERRORS.md).

## Certification and verification

- **Nothing is certified.** go-sunspec has not been certified by the SunSpec Alliance or anyone else, and no conformance claim is made.
- **Two independent implementations are in the test suite, on a limited set of models.** pysunspec2 and cB-Sigma's sunspec and sunspec-sim cover models 1, 101, 103, 111, 120, 123, 160, 701, 702, 704 and 705 ([INTEROPERABILITY.md](INTEROPERABILITY.md)). The other tests read the library's own `Server` and the [`testutil`](testutil/) fixture. Encoder and decoder share the same schemas, so a misreading of any other model definition would not show up in a round trip. No real device has been tested.
- **No device-compatibility list exists.** CI uses no hardware. Which vendors and firmware versions work is not recorded here.
- **Fuzzing is short.** CI runs each fuzz target for 10 seconds. There is no scheduled long run. `make soak` and `make fuzz FUZZTIME=...` are run by hand.
- CI runs on Linux only (`ubuntu-latest`).

## Models

- The registry holds the 112 models of a snapshot of [sunspec/models](https://github.com/sunspec/models). A weekly workflow reports upstream changes; it changes nothing by itself.
- Models without a schema, vendor models among them, are discovered and returned as raw registers: `ReadModel` adds a warning, `ReadPoint` finds no point.
- Points are decoded and scaled, not interpreted. The library does not check that values are plausible or judge what an enum or bitfield means beyond its symbol name.

## Reading

- The client reads with function codes 03 and 04 only, except `WritePoint`, which writes one top-level `RW` point with FC 16. It does not write points of repeating groups, several points in one request, or with FC 06. Between its read of the scale factor and the write the device may change it.
- **A model read is not atomic across requests.** Modbus allows 125 registers per request, so a longer model takes several, and a device can change between them. `ReadModel` compares the ID and length registers with discovery, not the data.
- `DecodeModel` does no such comparison: it uses the schema it is given, whatever the ID and length registers say.
- `ReadPoint` returns the first point of that name, so for a repeating group the one of the first instance.
- A point whose scale factor is missing, not implemented or outside -10..10 stays unscaled (`ScaledValue` is nil; out of range also adds a warning).
- String points are returned byte for byte as the device sends them, after trimming trailing NULs and spaces: control and escape characters and invalid UTF-8 are not removed. Escape them before showing them on a terminal. A group that does not fit the registers, or registers left over, is a warning, not an error.
- There is no polling, subscription or caching: every read goes to the device. Timing is that of the Modbus client and the device; nothing here is real-time.
- Transport, retries, logging, metrics and connection lifecycle belong to the injected `*modbus.Client`.

## Serving

- `Server` holds registers. It models no device behaviour: an accepted write is stored as it is, and acting on it is up to `ServerOptions.OnWrite`.
- Function codes 6 and 16 write; 22 and 23 are not supported. Coils and discrete inputs do not exist. Writes must cover whole `RW` points of a model served from values.
- A client that needs several requests for a model of more than 125 registers can see different updates in them.
- Modbus has no authentication of its own: see [SECURITY.md](SECURITY.md).

## Device files

- `devicefile.Load` reads regular files of up to 16 MiB (`MaxFileSize`). The check that the path is a regular file is made before it is opened and is not atomic with the open: a path swapped for a named pipe in between can block the open. Whatever is opened, no more than 16 MiB and one byte is read.
- `File.WriteFile` replaces the file by a rename. The new file has mode 0600 and a new inode: other links to the old file keep the old content, and owner, mode and extended attributes of the old file are not carried over. The directory is not synced after the rename, so after a power loss the rename can be lost (the old file is then intact). A temporary file `.<name>.tmp*` can be left behind only when the process dies between creating and renaming it.
