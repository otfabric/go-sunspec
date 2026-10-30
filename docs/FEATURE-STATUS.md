# Support and feature matrix

What go-sunspec supports and how it is tested. Status: v0.7.1. API contract: [API.md](../API.md). Out of scope and unverified: [LIMITATIONS.md](../LIMITATIONS.md).

None of this is a conformance claim: nothing is certified.

## 1. Versions

| go-sunspec | Go | go-modbus | Models | Supported |
|---|---|---|---|---|
| **v0.7.1** | 1.23 or later (CI: 1.23 to 1.26) | v1.9.0 | 112, snapshot of sunspec/models | **yes** |

## 2. Platforms

CI runs on Linux only (`ubuntu-latest`). Linux arm64 and arm, macOS arm64 and Windows amd64 are built by `make cross`, not tested.

## 3. Feature support

✅ supported · ◐ partly (see the notes) · — not implemented. **E2E**: the library's `Server` read through the client path over loopback TCP. No row has independent evidence: see [INTEROPERABILITY.md](../INTEROPERABILITY.md).

| Feature | Client | Server | E2E |
|---|:---:|:---:|:---:|
| Detection of the `SunS` marker at the common base addresses | ✅ | ✅ | ✅ |
| Model chain discovery | ✅ | ✅ | ✅ |
| Decoding and scaling points, repeating and nested groups | ✅ | n/a | ✅ chaos |
| Encoding models | n/a | ✅ `EncodeModel` | ✅ round trip |
| Models without a schema, vendor models | ◐ raw registers | — | ✅ |
| Reads over Modbus: function codes 03, 04 | ✅ | ✅ | ✅ |
| Writes: function codes 06, 16 | — client does not write | ✅ whole `RW` points | ✅ |
| Function codes 22, 23; coils and discrete inputs | — | — | n/a |
| Device files (`devicefile`) | n/a | ✅ | fuzz |
| Polling, subscription, caching | — | — | n/a |
