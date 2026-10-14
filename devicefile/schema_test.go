// SPDX-License-Identifier: MIT

package devicefile

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	compileOnce sync.Once
	compiled    *jsonschema.Schema
	compileErr  error
)

// validate checks a JSON document against the published schema.
func validate(t *testing.T, data []byte) error {
	t.Helper()
	compileOnce.Do(func() {
		var doc interface{}
		if doc, compileErr = jsonschema.UnmarshalJSON(bytes.NewReader(Schema())); compileErr != nil {
			return
		}
		c := jsonschema.NewCompiler()
		if compileErr = c.AddResource(SchemaID, doc); compileErr != nil {
			return
		}
		compiled, compileErr = c.Compile(SchemaID)
	})
	if compileErr != nil {
		t.Fatalf("the schema does not compile: %v", compileErr)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return compiled.Validate(inst)
}

// checkSchema fails the test when data is not valid against the schema.
func checkSchema(t *testing.T, what string, data []byte) {
	t.Helper()
	if err := validate(t, data); err != nil {
		t.Errorf("%s is not valid against the schema: %v\n%s", what, err, data)
	}
}

func TestSchemaDocument(t *testing.T) {
	var doc struct {
		Schema string                     `json:"$schema"`
		ID     string                     `json:"$id"`
		Defs   map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(Schema(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != "https://json-schema.org/draft/2020-12/schema" || doc.ID != SchemaID {
		t.Errorf("$schema %q, $id %q", doc.Schema, doc.ID)
	}
	for _, def := range []string{"model", "group", "points", "groups", "value", "register"} {
		if !bytes.Contains(doc.Defs[def], []byte(`"description"`)) {
			t.Errorf("$defs/%s is missing or has no description", def)
		}
	}
	// The embedded schema is the file, and callers get a copy.
	onDisk, err := os.ReadFile("devicefile.schema.json")
	if err != nil || !bytes.Equal(onDisk, Schema()) {
		t.Errorf("Schema() differs from devicefile.schema.json (%v)", err)
	}
	s := Schema()
	s[0] = 'x'
	if Schema()[0] != '{' {
		t.Error("Schema() returns the embedded slice itself")
	}
}

// docExample returns the device file shown in the module's API.md.
func docExample(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile("../API.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	section := strings.Index(text, "\n## Device files")
	if section < 0 {
		t.Fatal("API.md has no section on device files")
	}
	text = text[section:]
	start := strings.Index(text, "```json\n")
	if start < 0 {
		t.Fatal("API.md has no JSON example of a device file")
	}
	example := text[start+len("```json\n"):]
	return example[:strings.Index(example, "```")]
}

// TestSchemaAcceptsValidFiles validates hand-written files against the
// schema; the files Marshal writes are validated where they are made
// (captures, templates of every model, the golden files).
func TestSchemaAcceptsValidFiles(t *testing.T) {
	files := map[string]string{
		"the example device":       exampleDevice,
		"the example of API.md":    docExample(t),
		"an empty object":          `{}`,
		"a device without models":  `{"models": []}`,
		"a model without values":   `{"models": [{"id": 1}]}`,
		"an empty bitfield":        `{"models": [{"id": 103, "points": {"Evt1": []}}]}`,
		"a null group instance":    `{"models": [{"id": 160, "groups": {"module": [null, {}]}}]}`,
		"a repeated model":         `{"models": [{"id": 1}, {"id": 1}]}`,
		"testdata/example.json":    "",
		"testdata/der.json":        "",
		"base address 0, unit 255": `{"unit_id": 255, "base_address": 0}`,
	}
	for name, text := range files {
		if text == "" {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			text = string(data)
		}
		checkSchema(t, name, []byte(text))
		// What the schema accepts here, the package serves, and writes as a
		// file that is valid again.
		file, err := Parse([]byte(text))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if _, err := file.Server(nil); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		data, err := file.Marshal()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// A file without "models" is written with "models": null, which
		// Parse reads back and the schema does not describe.
		if file.Models != nil {
			checkSchema(t, name+", written again", data)
		}
	}
}

// TestSchemaRejectsInvalidFiles runs the files the package refuses for their
// form through the schema: it refuses them too. Files that are refused for
// what the schema cannot know (the model definitions, the address space)
// are valid against it.
func TestSchemaRejectsInvalidFiles(t *testing.T) {
	for _, tc := range fileErrors {
		if !json.Valid([]byte(tc.content)) {
			continue
		}
		err := validate(t, []byte(tc.content))
		switch {
		case tc.name == "duplicate point":
			// A JSON Schema validator sees an object, not its text.
		case tc.name == "unknown model":
			// Which models have a definition is not in the schema.
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
		case tc.name == "empty registers":
			// Refused by the layout, and by the schema.
			if err == nil {
				t.Errorf("%s: valid against the schema", tc.name)
			}
		case tc.is == ErrSyntax || tc.is == ErrInvalid:
			if err == nil {
				t.Errorf("%s: valid against the schema, refused by the package", tc.name)
			}
		default:
			if err != nil {
				t.Errorf("%s: the schema refuses what only the model definitions can decide: %v", tc.name, err)
			}
		}
	}
	// Unknown members are refused at every level.
	for _, text := range []string{
		`{"$schema": "x"}`,
		`{"models": [{"id": 1, "Points": {}}]}`,
		`{"models": [{"id": 160, "groups": {"module": [{"id": 1}]}}]}`,
		`{"models": [{"id": 160, "groups": {"module": {}}}]}`,
		`{"models": [{"id": 70000}]}`,
		`{"models": [{"id": 1, "points": {"Mn": [true]}}]}`,
		`{"models": {}}`,
		`[]`,
	} {
		if validate(t, []byte(text)) == nil {
			t.Errorf("%s: valid against the schema", text)
		}
	}
}
