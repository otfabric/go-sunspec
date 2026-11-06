# Support and feature matrix

What go-sunspec supports and how it is tested. Status: v0.10.0. API contract: [API.md](../API.md). Out of scope and unverified: [LIMITATIONS.md](../LIMITATIONS.md).

None of this is a conformance claim: nothing is certified.

## 1. Versions

| go-sunspec | Go | go-modbus | Models | Supported |
|---|---|---|---|---|
| **v0.10.0** | 1.26 or later (CI: 1.26 and 1.27) | v1.11.1 | 112, snapshot of sunspec/models | **yes** |

## 2. Platforms

CI runs on Linux only (`ubuntu-latest`). Linux arm64 and arm, macOS arm64 and Windows amd64 are built by `make cross`, not tested.

## 3. Feature support

✅ supported · ◐ partly (see the notes) · — not implemented. **E2E**: the library's `Server` read through the client path over loopback TCP. **Interop**: against pysunspec2 and cB-Sigma, see [INTEROPERABILITY.md](../INTEROPERABILITY.md).

| Feature | Client | Server | E2E |
|---|:---:|:---:|:---:|
| Detection of the `SunS` marker at the common base addresses | ✅ | ✅ | ✅ |
| Model chain discovery | ✅ | ✅ | ✅ |
| Decoding and scaling points, repeating and nested groups | ✅ | n/a | ✅ chaos |
| Encoding models | n/a | ✅ `EncodeModel` | ✅ round trip |
| Models without a schema, vendor models | ◐ raw registers | — | ✅ |
| Reads over Modbus: function codes 03, 04 | ✅ | ✅ | ✅ |
| Writes: function codes 06, 16 | ◐ `WritePoint`: one top-level `RW` point, FC 16 only | ✅ whole `RW` points | ✅ chaos, fuzz, interop |
| Function codes 22, 23; coils and discrete inputs | — | — | n/a |
| Device files (`devicefile`) | n/a | ✅ | fuzz |
| Polling, subscription, caching | — | — | n/a |
