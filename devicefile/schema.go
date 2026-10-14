// SPDX-License-Identifier: MIT

package devicefile

import (
	_ "embed" // The schema is embedded.
)

// SchemaID is the "$id" of the JSON Schema of the device file format.
const SchemaID = "https://otfabric.io/schemas/sunspec/devicefile-1.schema.json"

//go:embed devicefile.schema.json
var schema []byte

// Schema returns the JSON Schema (draft 2020-12) of the device file format,
// the content of devicefile.schema.json. The slice is a copy.
//
// The schema describes the form of a file, as Parse checks it, with one
// addition: values Parse leaves to ServerModels (a model without "id", both
// "registers" and "points", a point value that is an object or a boolean)
// are invalid against the schema too. Whether the models and points exist
// and the values fit is not something the schema knows.
func Schema() []byte {
	return append([]byte(nil), schema...)
}
