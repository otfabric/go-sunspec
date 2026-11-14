# Limitations

What go-sunspec does not do, and what has not been verified. For the API contract see [API.md](API.md), for errors [ERRORS.md](ERRORS.md).

## Certification and verification

- **Nothing is certified.** go-sunspec has not been certified by the SunSpec Alliance or anyone else, and no conformance claim is made.
- **Two independent implementations are in the test suite, on a limited set of models.** pysunspec2 and cB-Sigma's sunspec and sunspec-sim cover models 1, 101, 103, 111, 120, 123, 160, 701, 702, 704 and 705 ([INTEROPERABILITY.md](INTEROPERABILITY.md)). The other tests read the library's own `Server` and the [`testutil`](testutil/) fixture. Encoder and decoder share the same schemas, so a misreading of any other model definition would not show up in a round trip. No real device has been tested.
- **No device-compatibility list exists.** CI uses no hardware. Which vendors and firmware versions work is not recorded here.
- **Fuzzing is short on a pull request.** CI runs each fuzz target for 10 seconds there; the nightly workflow runs each for 5 minutes. `make soak` is run by hand.
- CI runs on Linux only (`ubuntu-latest`): amd64, and in the nightly workflow the tests as 386 binaries, where an `int` has 32 bits. No other platform is tested.
- **Which edition and clause of the SunSpec specification each rule follows is not recorded**: [SPECIFICATIONS.md](SPECIFICATIONS.md) maps the rules to the model definitions and to pysunspec2, and says where no source was identified.

## Models

- The registry holds the 112 models of a snapshot of [sunspec/models](https://github.com/sunspec/models); the commit, the date and the checksums are in [models/README.md](models/README.md). A weekly workflow reports upstream changes, against the branch `master`; it changes nothing by itself.
- Models without a schema, vendor models among them, are discovered and returned as raw registers: `ReadModel` adds a warning, `ReadPoint` finds no point.
- Points are decoded and scaled, not interpreted. The library does not check that values are plausible or judge what an enum or bitfield means beyond its symbol name.

## Reading

- The client reads with function codes 03 and 04 only, except `WritePoint`, which writes one top-level `RW` point with FC 16. It does not write points of repeating groups, several points in one request, or with FC 06. Between its read of the scale factor and the write the device may change it. An engineering value is not written while the device reports the point's scale factor as not implemented or outside -10..10 (`ErrPointNotWritable`): only the point's registers, as `[]uint16`, can be written then.
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
- `ServerOptions.OnWrite` judges a write before it takes effect, without the `Server`'s lock held. When the model changes while it runs, the write is refused with exception 4: `OnWrite` has then seen a write that did not take effect, and is not told. An `OnWrite` that acts on what it sees (forwards it, records it) has to allow for that.
- The function given to `Update` can be called more than once for one update, so it cannot count calls or do something that must happen once.
- `Serve` gives connections five seconds to end when its context is done. A request still being served then (an `OnWrite` that does not return) is not waited for: `Serve` returns an error matching `context.DeadlineExceeded`, and that request's goroutine ends when the callback returns.
- Modbus has no authentication of its own: see [SECURITY.md](SECURITY.md).

## Memory

Figures for 64-bit platforms, measured with `runtime.MemStats` around one call; the tests named pin a budget above them.

- **Decoding costs far more memory than the registers.** A decoded point carries its name, type, units and values, so `DecodeModel` allocates up to about 925 bytes for each register it decodes, and `DecodedModel.Values` up to about 410 more. Registers after the last group of a model are not decoded and cost nothing. The worst case for one model is a model with a repeating group at the longest length a device can announce, 65535 data registers (128 KiB on the wire), with every scale factor out of range, which adds a warning for each point:

  | Model | `DecodeModel` | `Values` |
  |---|---|---|
  | 64410, the highest | 60.5 MB | 5.3 MB |
  | 135 to 144, for example 144 | 49.6 MB | 6.2 MB |
  | 705 (nested curves) | 48.3 MB | 13.1 MB |
  | 160 | 25.5 MB | 3.6 MB |
  | 3, 303, 806 | 21 to 23 MB | 26 to 27 MB (303: 26.9 MB, the highest) |
  | Models without a repeating group (1, 101, 103, 120, 123, 701, …) | under 0.1 MB | under 0.1 MB |

  A device map has at most 65536 registers in all, so these are also the bounds for `ReadAll` of one device. What a device announces beyond the registers it has costs nothing: a count point of 65535, or a 32-bit one of 4294967294, decodes the instances the registers hold. `TestDecodeModelAllocationBudget` and `TestDecodeCountAllocationBudget` allow 2048 bytes per register for `DecodeModel` and 768 for `Values`.
- **A device file takes up to about 30 times its size to parse.** `devicefile.Parse` of a file of the maximum size (16 MiB) that holds nothing but empty group instances, the shape that costs the most, allocates about 490 MB. `File.ServerModels` and `File.Server` add little to that: they convert the groups along the model's schema, refuse a nested group the schema does not have before converting its instances, and stop at 65535 group instances per model. `TestFileAllocationBudget` allows 40 bytes per byte of file for `Parse` and `File.Server` together. A file that gives every register of a whole device map is well under 1 MiB, and `MaxFileSize` is 16 MiB: on a host with little memory, check the size of a file from a source you do not trust before `Load`.

## Device files

- `devicefile.Load` reads regular files of up to 16 MiB (`MaxFileSize`). The check that the path is a regular file is made before it is opened and is not atomic with the open: a path swapped for a named pipe in between can block the open. Whatever is opened, no more than 16 MiB and one byte is read.
- `File.WriteFile` replaces the file by a rename. The new file has mode 0600 and a new inode: other links to the old file keep the old content, and owner, mode and extended attributes of the old file are not carried over. The directory is not synced after the rename, so after a power loss the rename can be lost (the old file is then intact). A temporary file `.<name>.tmp*` can be left behind only when the process dies between creating and renaming it.
