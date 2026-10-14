// SPDX-License-Identifier: MIT

// Package devicefile reads and writes device files: JSON documents that
// describe a SunSpec device by its models and their point values.
//
// A device file is what "sunspecctl serve" runs, what "sunspecctl capture"
// and "sunspecctl template" write, and what the OT Fabric SunSpec Explorer
// exports. This package is the one implementation of the format:
//
//	{
//	  "unit_id": 1,
//	  "base_address": 40000,
//	  "models": [
//	    {"id": 1, "points": {"Mn": "Acme", "Md": "PV-5000"}},
//	    {"id": 103, "points": {"W": 4200, "W_SF": 0, "St": "MPPT"}},
//	    {"id": 160, "groups": {"module": [{"points": {"DCW": 2100}}]}},
//	    {"id": 64001, "registers": [1, 2, 3]}
//	  ]
//	}
//
// A model is given by point values, which are encoded with the registry's
// schema of the model, or by its raw data registers, which is the only way
// to describe a model without a schema. Points take their engineering value
// (the encoder applies the scale factor), enum and bitfield points their
// symbol names or a number, any point a list of raw registers, and null
// stands for "not implemented". Repeating groups are lists of instances
// under "groups". Schema returns a JSON Schema of the format.
//
// # Entry points
//
//   - Parse / Load — read a device file; errors carry the line and column
//   - File.ServerModels / File.Server — turn it into a served device
//   - Capture / FromDecoded — describe a device that was read
//   - Template — a model with every point present and null
//   - File.Marshal / Write / WriteFile — write the file as indented JSON
//
// # Errors
//
// Text that is not a device file is reported with *SyntaxError (matching
// ErrSyntax), a file that does not describe a device that can be built with
// an error matching ErrInvalid; see ERRORS.md of the module.
package devicefile
