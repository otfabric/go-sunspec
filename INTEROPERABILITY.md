# Interoperability

**One independent SunSpec implementation is in the test suite: pysunspec2, the SunSpec Alliance's Python stack. Nothing is certified.** go-sunspec has not been certified by the SunSpec Alliance or anyone else, and no device-compatibility list exists.

`make interop` and the Interop workflow run [`interop/`](interop/) against `sunspec-interop-pysunspec2` v0.1.0 (pysunspec2 1.3.7, pinned by digest):

| Test | Direction | Shows |
|---|---|---|
| `TestGoReadsPysunspec2Device` | go-sunspec reads a device pysunspec2 encoded | The model chain, and every raw and scaled point, match what pysunspec2's client reads from the same device |
| `TestPysunspec2ReadsGoServer` | pysunspec2 reads `Server` | pysunspec2 finds the device and reads the same points as go-sunspec, and the registers equal those pysunspec2 encodes for the same values |
| `TestPysunspec2WritesGoServer` | pysunspec2 writes `Server` | A write of a read-write point lands; a write of a read-only point is refused |

Each runs on the fixtures of sunspec-interop: models 1, 101, 103, 111, 120, 123, 160 and 701, at base addresses 0, 40000 and 50000, with a repeating group, not-implemented values, 32-bit floats, 64-bit accumulators and scale factors from -3 to 3.

What that does not show: the device side of the image is pysunspec2's register encoding behind a small Modbus TCP layer of the harness, because pysunspec2 has no server, so it says nothing about Modbus transport. Other models, vendor models, nested groups, Modbus RTU and real devices are not in the suite. go-sunspec's client does not write, so no test covers a write from go-sunspec.

What the code is built on:

| Dependency | Role | Independent of this code | Evidence |
|---|---|---|---|
| [sunspec/models](https://github.com/sunspec/models) | source of the 112 model schemas in `models/`, compiled into `registry` | yes, for the schemas | `make models-check`; weekly workflow reports upstream changes |
| go-modbus v1.9.0 | Modbus client and TCP server in the tests | no, OT Fabric; its own interop is in that repository | `go.mod` |

Those are inputs, not interoperability evidence: reading a schema from upstream does not show that a real device or another SunSpec stack agrees with this decoder. Only the table above is.
