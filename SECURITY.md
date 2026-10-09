# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `go-sunspec` or `sunspecctl`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.com**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/go-sunspec/security) tab → **Advisories** → **Report a vulnerability**), with:

- A description of the vulnerability
- Steps to reproduce the issue, ideally a minimal program or test
- The affected version(s) (`sunspecctl version` output for the CLI)
- Any potential impact you've identified

We will acknowledge your report within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | Best effort |

## Security Considerations

### SunSpec Modbus Has No Built-In Security

SunSpec devices are read over Modbus, which carries no authentication and no encryption on TCP, UDP, RTU or ASCII: anyone who can reach a device can read and write it, and anyone on the path can read or alter the traffic.

- **Use this library and `sunspecctl` only on devices and networks you own or are authorized to access**
- **Keep Modbus traffic on isolated, trusted networks**; never expose port 502 of an inverter, meter or battery to the Internet
- Across untrusted networks, use Modbus over TLS (`tcp+tls://`, supported by go-modbus) where the device supports it, or a VPN or secured gateway

### Data Read From Devices Is Untrusted Input

- Model IDs, lengths, strings and values come from the device. The library bounds discovery (`MaxModels`, `MaxAddressSpan`) and validates lengths before decoding, but your application should still treat decoded values as untrusted: validate ranges before using them for control decisions, and escape strings (manufacturer, model, serial number) before putting them in HTML, SQL or shell commands
- Decoded data and `sunspecctl` output can contain serial numbers, network addresses and production figures; treat them as sensitive operational data when storing or sharing them

### Reading Only

This package reads and decodes; it does not write SunSpec control points. If you write registers yourself with the go-modbus client, remember that DER controls change the behaviour of physical equipment connected to the grid.

### Model Definitions

The model definitions compiled into the registry come from the official [sunspec/models](https://github.com/sunspec/models) repository and are reviewed as a diff when they are updated. They determine how registers are interpreted, so only take them from that source.

### Dependencies

`make check` runs `govulncheck`. Vulnerabilities reported in the Go standard library are fixed by building with a patched Go release.

### Verifying Downloads

`sunspecctl` release archives are published with a SHA-256 checksum file. Verify the checksum before installing.
