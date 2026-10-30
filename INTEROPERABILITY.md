# Interoperability

**No independent SunSpec implementation is in the test suite, and nothing is certified.** go-sunspec has not been certified by the SunSpec Alliance or anyone else, and no device-compatibility list exists. The tests read the library's own `Server` and the `testutil` fixture ([LIMITATIONS.md](LIMITATIONS.md)).

What the code is built on:

| Dependency | Role | Independent of this code | Evidence |
|---|---|---|---|
| [sunspec/models](https://github.com/sunspec/models) | source of the 112 model schemas in `models/`, compiled into `registry` | yes, for the schemas | `make models-check`; weekly workflow reports upstream changes |
| go-modbus v1.9.0 | Modbus client and TCP server in the tests | no, OT Fabric; its own interop is in that repository | `go.mod` |

Those are inputs, not interoperability evidence: reading a schema from upstream does not show that a real device or another SunSpec stack agrees with this decoder.
