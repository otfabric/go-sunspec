// SPDX-License-Identifier: MIT

// Package sunspec reads SunSpec devices over Modbus.
//
// It discovers SunSpec model chains, decodes standard point types, resolves
// scale factors, and decodes nested and repeating groups. Schemas for standard models are
// compiled into package registry.
//
// # Scope
//
// This package is a domain layer on top of [github.com/otfabric/go-modbus].
// Transport, retries, logging, metrics, and connection lifecycle belong to the
// injected *modbus.Client. This package does not open or close connections.
//
// # Entry points
//
//   - Detect — probe for a SunSpec marker
//   - Discover / Open — enumerate models and return a Device
//   - Device.ReadAll / ReadModel / ReadModelByID / ReadPoint — read and decode
//   - DecodeModel — decode a register slice against a schema (no I/O)
//
// # Ownership
//
// The caller creates, opens, and closes the *modbus.Client. Device holds a
// reference to that client and does not take ownership. Closing the client
// invalidates further Device operations.
//
// # Concurrency
//
// Device methods are safe for concurrent use to the same extent as the
// underlying *modbus.Client. The compiled registry is safe for concurrent
// reads after package init; callers must not call registry.Register after init.
//
// # Errors
//
// See ERRORS.md for sentinels, typed errors, and relationship to Modbus errors.
package sunspec
