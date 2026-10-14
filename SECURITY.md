# Security Policy

## Reporting a Vulnerability

The `sunspecctl` command-line tool has its own repository and policy: [otfabric/sunspecctl](https://github.com/otfabric/sunspecctl/blob/main/SECURITY.md).

If you discover a security vulnerability in `go-sunspec`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.com**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/go-sunspec/security) tab → **Advisories** → **Report a vulnerability**), with:

- A description of the vulnerability
- Steps to reproduce the issue, ideally a minimal program or test
- The affected version(s)
- Any potential impact you've identified

We will acknowledge your report within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | Best effort |

## Security Considerations

### SunSpec Modbus Has No Built-In Security

SunSpec devices are read and served over Modbus, which carries no authentication and no encryption on TCP, UDP, RTU or ASCII: anyone who can reach a device can read and write it, and anyone on the path can read or alter the traffic.

- **Use this library only on devices and networks you own or are authorized to access**
- **Keep Modbus traffic on isolated, trusted networks**; never expose port 502 of an inverter, meter or battery to the Internet
- Across untrusted networks, use Modbus over TLS (`tcp+tls://`, supported by go-modbus) where the device supports it, or a VPN or secured gateway

### Data Read From Devices Is Untrusted Input

- Model IDs, lengths, strings and values come from the device. The library bounds discovery (`MaxModels`, `MaxAddressSpan`) and validates lengths before decoding, but your application should still treat decoded values as untrusted: validate ranges before using them for control decisions, and escape strings (manufacturer, model, serial number) before putting them in HTML, SQL or shell commands
- Decoded data can contain serial numbers, network addresses and production figures; treat them as sensitive operational data when storing or sharing them

### Reading Devices

As a client this package reads and decodes; it does not write SunSpec control points. It sends Modbus function codes 03 and 04 only. To make that a property of the connection, create the client with `modbus.Config.AllowedFunctionCodes: sunspec.ReadFunctionCodes()`: it then refuses every other function code before anything is sent. If you write registers yourself with the go-modbus client, remember that DER controls change the behaviour of physical equipment connected to the grid.

### Serving Devices

A `Server` answers every client that reaches it: Modbus has no authentication, so whoever can connect can read all of its registers and write its writable points.

- **Choose the listen address deliberately.** `tcp://127.0.0.1:1502` is reachable from the same host only; `tcp://0.0.0.0:...` from every network the host is on. Never expose a served device to the Internet
- **Writes come from untrusted clients.** The `Server` accepts any register value for a writable point; it checks which registers may be written, not what is written. When a written value drives anything real, validate it in `ServerOptions.OnWrite` (range, plausibility, which client) and refuse what you do not accept. `ServerWrite.ClientAddr` is what the transport reports, not an authenticated identity
- **Use TLS with client certificates** (`tcp+tls://`, configured on `modbus.ServerConfig`) when clients connect across a network you do not control, and limit `MaxClients` and the idle `Timeout` there
- A simulator that mimics a real device mimics its identity too: do not put one on a production network where a controller could mistake it for the equipment

### Model Definitions

The model definitions compiled into the registry come from the official [sunspec/models](https://github.com/sunspec/models) repository and are reviewed as a diff when they are updated. They determine how registers are interpreted, so only take them from that source.

### Dependencies

`make check` runs `govulncheck`. Vulnerabilities reported in the Go standard library are fixed by building with a patched Go release.
