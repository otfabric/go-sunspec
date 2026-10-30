// SPDX-License-Identifier: MIT

// Package sunspec reads and serves SunSpec devices over Modbus.
//
// It discovers SunSpec model chains, decodes standard point types, resolves
// scale factors, and decodes nested and repeating groups. In the other
// direction it encodes point values into a model's registers and serves a
// device built from such models. Schemas for standard models are compiled
// into package registry.
//
// # Scope
//
// This package is a domain layer on top of [github.com/otfabric/go-modbus].
// Transport, retries, logging, metrics, and connection lifecycle belong to the
// injected *modbus.Client (which Detect, Discover and Device only ever ask to
// read registers: function codes 03 and 04), and on the serving side to the *modbus.Server a
// Server is handed to. This package does not open or close connections; the
// one exception is the convenience Server.Serve, which runs a modbus.Server
// for the duration of a context.
//
// # Entry points
//
//   - Detect — probe for a SunSpec marker
//   - Discover / Open — enumerate models and return a Device
//   - Device.ReadAll / ReadModel / ReadModelByID / ReadPoint — read and decode
//   - Device.ReadModels — read several models, adjacent ones sharing requests
//   - Device.ModelByID / ModelsByID — find model instances by ID
//   - DecodeModel — decode a register slice against a schema (no I/O)
//   - EncodeModel — build a model's registers from values by point name (no I/O)
//   - NewServer — lay out a device from models and values; the Server is a
//     go-modbus RequestHandler, and its SetPoint / Update methods change
//     values while it is served
//   - ReadFunctionCodes — the function codes of a client that cannot write
//
// Package devicefile reads and writes a device as a JSON file: what a Server
// is built from, and what a Device that was read can be written as.
//
// # Ownership
//
// The caller creates, opens, and closes the *modbus.Client. Device holds a
// reference to that client and does not take ownership. Closing the client
// invalidates further Device operations.
//
// A Server owns its register map and nothing else. The caller creates,
// starts and stops the *modbus.Server that serves it.
//
// # Concurrency
//
// Device methods are safe for concurrent use to the same extent as the
// underlying *modbus.Client. Server methods are safe for concurrent use with
// each other and with the requests being served. The compiled registry is
// safe for concurrent reads after package init; callers must not call
// registry.Register after init.
//
// # Errors
//
// See ERRORS.md for sentinels, typed errors, and relationship to Modbus errors.
package sunspec
