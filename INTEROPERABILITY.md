# Interoperability

**Two independent SunSpec implementations are in the test suite: pysunspec2, the SunSpec Alliance's Python stack, and cB-Sigma's Go `sunspec` client with its `sunspec-sim` device. Nothing is certified.** go-sunspec has not been certified by the SunSpec Alliance or anyone else, and no device-compatibility list exists.

`make interop` and the Interop workflow run [`interop/`](interop/) against the images of [otfabric/sunspec-interop](https://github.com/otfabric/sunspec-interop) v0.3.0, pinned by digest: `sunspec-interop-pysunspec2` (pysunspec2 1.3.7) and `sunspec-interop-cbsigma` ([cB-Sigma/sunspec](https://github.com/cB-Sigma/sunspec) v0.3.0 and [cB-Sigma/sunspec-sim](https://github.com/cB-Sigma/sunspec-sim) v0.3.0). Each test runs against each of them:

| Test | Direction | Shows |
|---|---|---|
| `TestGoReadsPeerDevice` | go-sunspec reads a device the peer encoded | The model chain, and every raw and scaled point, including those of nested repeating groups and of groups that occur once, match what the peer's client reads from the same device |
| `TestPeerReadsGoServer` | the peer reads `Server` | The peer finds the device and reads the same points as go-sunspec, and the registers equal those the peer encodes for the same values |
| `TestPeerWritesGoServer` | the peer writes `Server` | A write of a read-write point lands; a write of a read-only point is refused. Points inside groups (the power factor of `PFWInj` in model 704; a point and the active point count of a volt-var curve in model 705) land too, and a read-only curve flag is refused |
| `TestGoWritesPeerDevice` | go-sunspec writes the device | `WritePoint` of a scaled read-write point and an enum lands, and the peer's client reads the raw values back; a read-only point is refused before anything is sent |
| `TestGoWritesPeerDER` | go-sunspec writes the DER device | `WritePoint` of an IEEE 1547 capacity setting (model 702 `WMax`, scale factor 1) and a curve adoption request (model 705 `AdptCrvReq`) lands, and the peer's client reads them back; the read-only `AdptCrvRslt` is refused before anything is sent |

Each runs on the fixtures of sunspec-interop: models 1, 101, 103, 111, 120, 123, 160, 701, 702, 704 and 705, at base addresses 0, 40000 and 50000, with repeating and nested repeating groups (the volt-var curves of model 705, counted by top-level points), groups that occur once, not-implemented values, signed setpoints, 32-bit floats, 64-bit accumulators and scale factors from -3 to 3.

What that does not show: pysunspec2 has no server, so its device is pysunspec2's register encoding behind a small Modbus TCP layer of the harness; sunspec-sim is cB-Sigma's own device and Modbus TCP server. Other models, vendor models, Modbus RTU and real devices are not in the suite. go-sunspec writes with `WritePoint` (since v0.10.0), which writes top-level points only, so writes inside groups are shown only in the direction peer → `Server`; that a device refuses a write of a read-only point is shown only for go-sunspec's `Server`, since `WritePoint` refuses it before sending. cbsigma leaves `pad` points out of its scan document, and prints scaled 64-bit accumulators as float64; the tests skip a missing `pad` and serve the raw value where no scale factor applies.

The same tests run against `sunspec-interop-otfabric`, a released go-sunspec behind the same contract, when `SUNSPEC_INTEROP_OTFABRIC_IMAGE` names it: the manual Self-interop workflow runs a commit against `:latest`, to find a change on the wire that breaks an earlier release. A pass is not independent verification: it is go-sunspec on both sides, and it is not in the table above.

What the code is built on:

| Dependency | Role | Independent of this code | Evidence |
|---|---|---|---|
| [sunspec/models](https://github.com/sunspec/models) | source of the 112 model schemas in `models/`, compiled into `registry` | yes, for the schemas | `make models-check`; weekly workflow reports upstream changes |
| go-modbus v1.14.5 | Modbus client and TCP server in the tests | no, OT Fabric; its own interop is in that repository | `go.mod` |

Those are inputs, not interoperability evidence: reading a schema from upstream does not show that a real device or another SunSpec stack agrees with this decoder. Only the table above is.
