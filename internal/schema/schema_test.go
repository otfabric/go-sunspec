// SPDX-License-Identifier: MIT

package schema

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// writeFiles creates the given files (name -> content) in a new temporary
// directory and returns its path.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRawSFUnmarshal(t *testing.T) {
	tests := []struct {
		in      string
		want    RawSF
		wantStr string
		wantErr bool
	}{
		{in: `"W_SF"`, want: RawSF{Ref: "W_SF", IsSet: true}, wantStr: "W_SF"},
		{in: `""`, want: RawSF{IsSet: true}, wantStr: ""},
		{in: `-2`, want: RawSF{IntVal: -2, IsLiteral: true, IsSet: true}, wantStr: "-2"},
		{in: `0`, want: RawSF{IsLiteral: true, IsSet: true}, wantStr: "0"},
		{in: `3`, want: RawSF{IntVal: 3, IsLiteral: true, IsSet: true}, wantStr: "3"},
		{in: `null`, want: RawSF{}, wantStr: ""},
		{in: ` null `, want: RawSF{}, wantStr: ""},
		{in: `1.5`, wantErr: true},
		{in: `true`, wantErr: true},
		{in: `[1]`, wantErr: true},
		{in: `{"ref":"W_SF"}`, wantErr: true},
	}
	for _, tc := range tests {
		var got RawSF
		err := json.Unmarshal([]byte(tc.in), &got)
		if tc.wantErr {
			if err == nil {
				t.Errorf("sf %s: no error, got %+v", tc.in, got)
			} else if !strings.Contains(err.Error(), "sf: expected string or int") {
				t.Errorf("sf %s: error %q does not explain the problem", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("sf %s: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("sf %s: got %+v, want %+v", tc.in, got, tc.want)
		}
		if s := got.String(); s != tc.wantStr {
			t.Errorf("sf %s: String() = %q, want %q", tc.in, s, tc.wantStr)
		}
	}
}

// Regression: an explicit "sf": null used to be recorded as a set, empty
// point reference, which the generator emitted as SF: "".
func TestPointWithNullOrAbsentSF(t *testing.T) {
	var points []PointDef
	in := `[{"name":"A","sf":null},{"name":"B"},{"name":"C","sf":"C_SF"},{"name":"D","sf":-1}]`
	if err := json.Unmarshal([]byte(in), &points); err != nil {
		t.Fatal(err)
	}
	want := []RawSF{{}, {}, {Ref: "C_SF", IsSet: true}, {IntVal: -1, IsLiteral: true, IsSet: true}}
	for i, p := range points {
		if p.SF != want[i] {
			t.Errorf("point %s: SF = %+v, want %+v", p.Name, p.SF, want[i])
		}
	}
}

func TestRawCountUnmarshal(t *testing.T) {
	tests := []struct {
		in        string
		want      RawCount
		fixed     int
		repeating bool
		wantErr   bool
	}{
		{in: `1`, want: RawCount{IntVal: 1, IsSet: true}, fixed: 1, repeating: false},
		{in: `0`, want: RawCount{IsSet: true}, fixed: 0, repeating: true},
		{in: `4`, want: RawCount{IntVal: 4, IsSet: true}, fixed: 4, repeating: true},
		{in: `"N"`, want: RawCount{StringVal: "N", IsString: true, IsSet: true}, repeating: true},
		{in: `"1"`, want: RawCount{StringVal: "1", IsString: true, IsSet: true}, repeating: true},
		// null is the same as no count at all: exactly once.
		{in: `null`, want: RawCount{}, fixed: 1, repeating: false},
		{in: ` null `, want: RawCount{}, fixed: 1, repeating: false},
		{in: `1.5`, wantErr: true},
		{in: `false`, wantErr: true},
		{in: `[]`, wantErr: true},
	}
	for _, tc := range tests {
		var got RawCount
		err := json.Unmarshal([]byte(tc.in), &got)
		if tc.wantErr {
			if err == nil {
				t.Errorf("count %s: no error, got %+v", tc.in, got)
			} else if !strings.Contains(err.Error(), "count: expected int or string") {
				t.Errorf("count %s: error %q does not explain the problem", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("count %s: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("count %s: got %+v, want %+v", tc.in, got, tc.want)
		}
		if got.IsRepeating() != tc.repeating {
			t.Errorf("count %s: IsRepeating() = %v, want %v", tc.in, got.IsRepeating(), tc.repeating)
		}
		if !got.IsString && got.Fixed() != tc.fixed {
			t.Errorf("count %s: Fixed() = %d, want %d", tc.in, got.Fixed(), tc.fixed)
		}
	}

	// A group without a count occurs exactly once (the SunSpec schema
	// default), which is different from an explicit count of 0.
	var g GroupDef
	if err := json.Unmarshal([]byte(`{"name":"g"}`), &g); err != nil {
		t.Fatal(err)
	}
	if g.Count != (RawCount{}) || g.Count.IsSet || g.Count.Fixed() != 1 || g.Count.IsRepeating() {
		t.Errorf("absent count = %+v, Fixed() = %d, IsRepeating() = %v", g.Count, g.Count.Fixed(), g.Count.IsRepeating())
	}
	if err := json.Unmarshal([]byte(`{"name":"g","count":0}`), &g); err != nil {
		t.Fatal(err)
	}
	if !g.Count.IsSet || g.Count.Fixed() != 0 || !g.Count.IsRepeating() {
		t.Errorf("count 0 = %+v", g.Count)
	}
}

func TestIDFromFilename(t *testing.T) {
	tests := map[string]int{
		"model_1.json":     1,
		"model_64001.json": 64001,
		"model_007.json":   7,
		"model_.json":      0,
		"model_abc.json":   0,
		"model_12x.json":   0,
		"model_1.5.json":   0,
		"smdx_00001.xml":   0,
		"":                 0,
		"12.json":          12,
		"model_12":         12,
	}
	for name, want := range tests {
		if got := idFromFilename(name); got != want {
			t.Errorf("idFromFilename(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestIDFromPoints(t *testing.T) {
	tests := []struct {
		name   string
		points []PointDef
		want   int
	}{
		{"no points", nil, 0},
		{"float64 as decoded from JSON", []PointDef{{Name: "ID", Value: float64(103)}}, 103},
		{"int", []PointDef{{Name: "ID", Value: 160}}, 160},
		{"ID not first", []PointDef{{Name: "L", Value: float64(9)}, {Name: "ID", Value: float64(2)}}, 2},
		{"ID without value", []PointDef{{Name: "ID"}}, 0},
		{"non-numeric value", []PointDef{{Name: "ID", Value: "103"}}, 0},
		{"no ID point", []PointDef{{Name: "Id", Value: float64(5)}, {Name: "L", Value: float64(9)}}, 0},
		{"first usable ID wins", []PointDef{{Name: "ID", Value: "x"}, {Name: "ID", Value: float64(4)}}, 4},
	}
	for _, tc := range tests {
		if got := idFromPoints(tc.points); got != tc.want {
			t.Errorf("%s: idFromPoints = %d, want %d", tc.name, got, tc.want)
		}
	}
}

const fullModelJSON = `{
  "id": 901,
  "group": {
    "name": "demo", "label": "Demo", "desc": "A demo model", "type": "group",
    "points": [
      {"name": "ID", "type": "uint16", "size": 1, "mandatory": "M", "static": "S", "value": 901},
      {"name": "L", "type": "uint16", "size": 1, "mandatory": "M", "static": "S"},
      {"name": "W", "label": "Watts", "desc": "AC power", "type": "int16", "size": 1, "sf": "W_SF", "units": "W", "access": "RW"},
      {"name": "W_SF", "type": "sunssf", "size": 1},
      {"name": "Hz", "type": "uint16", "size": 1, "sf": -2, "units": "Hz"},
      {"name": "St", "type": "enum16", "size": 1, "symbols": [
        {"name": "OFF", "value": 1, "label": "Off", "desc": "Not running"},
        {"name": "ON", "value": 2}
      ]}
    ],
    "groups": [
      {"name": "rep", "label": "Rep", "type": "group", "count": "N", "points": [
        {"name": "V", "type": "uint16", "size": 1}
      ], "groups": [
        {"name": "inner", "type": "group", "count": 3, "points": [{"name": "X", "type": "uint16", "size": 1}]}
      ]}
    ]
  }
}`

func TestParseDirFullModel(t *testing.T) {
	dir := writeFiles(t, map[string]string{"model_901.json": fullModelJSON})
	models, err := ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1", len(models))
	}
	m := models[0]
	if m.ID != 901 {
		t.Errorf("ID = %d, want 901", m.ID)
	}
	g := m.Group
	if g.Name != "demo" || g.Label != "Demo" || g.Desc != "A demo model" || g.Type != "group" {
		t.Errorf("group = %+v", g)
	}
	if len(g.Points) != 6 {
		t.Fatalf("got %d points, want 6", len(g.Points))
	}

	id := g.Points[0]
	if id.Name != "ID" || id.Type != "uint16" || id.Size != 1 || id.Mandatory != "M" || id.Static != "S" || id.Value != float64(901) {
		t.Errorf("ID point = %+v", id)
	}
	w := g.Points[2]
	wantW := PointDef{
		Name: "W", Label: "Watts", Desc: "AC power", Type: "int16", Size: 1,
		SF: RawSF{Ref: "W_SF", IsSet: true}, Units: "W", Access: "RW",
	}
	if !reflect.DeepEqual(w, wantW) {
		t.Errorf("W point = %+v, want %+v", w, wantW)
	}
	if sf := g.Points[3].SF; sf.IsSet {
		t.Errorf("W_SF point has a scale factor: %+v", sf)
	}
	if sf := g.Points[4].SF; sf != (RawSF{IntVal: -2, IsLiteral: true, IsSet: true}) {
		t.Errorf("Hz scale factor = %+v, want literal -2", sf)
	}
	wantSyms := []SymbolDef{{Name: "OFF", Value: 1, Label: "Off", Desc: "Not running"}, {Name: "ON", Value: 2}}
	if !reflect.DeepEqual(g.Points[5].Symbols, wantSyms) {
		t.Errorf("symbols = %+v, want %+v", g.Points[5].Symbols, wantSyms)
	}

	if len(g.Groups) != 1 {
		t.Fatalf("got %d nested groups, want 1", len(g.Groups))
	}
	rep := g.Groups[0]
	if rep.Name != "rep" || rep.Label != "Rep" || rep.Count != (RawCount{StringVal: "N", IsString: true, IsSet: true}) || len(rep.Points) != 1 {
		t.Errorf("repeating group = %+v", rep)
	}
	if len(rep.Groups) != 1 || rep.Groups[0].Name != "inner" || rep.Groups[0].Count != (RawCount{IntVal: 3, IsSet: true}) {
		t.Errorf("nested group = %+v", rep.Groups)
	}
}

func TestParseDirSelectsSortsAndDerivesIDs(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		// Explicit id wins over the file name.
		"model_30.json": `{"id": 300, "group": {"name": "explicit"}}`,
		// No id: taken from the file name, even though an ID point exists.
		"model_20.json": `{"group": {"name": "from_filename", "points": [{"name": "ID", "value": 999}]}}`,
		// No id, no number in the file name: taken from the ID point.
		"model_custom.json": `{"group": {"name": "from_point", "points": [{"name": "ID", "value": 7}]}}`,
		// Nothing to go on: ID stays 0 and sorts first.
		"model_unknown.json": `{"group": {"name": "no_id"}}`,
		// Not model files.
		"schema.json":       `{"not": "a model"}`,
		"README.md":         "# models",
		"model_5.json.bak":  `not json`,
		"mymodel_6.json":    `not json`,
		"model_readme.txt":  `not json`,
		"Model_9.json":      `not json`,
		"model_5.yaml":      `not json`,
		"another_file.json": `{`,
	})
	// A directory that looks like a model file is not a model file either.
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subdir", "model_77.json"), []byte(`{"group":{"name":"nested"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	models, err := ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	type idName struct {
		id   int
		name string
	}
	var got []idName
	for _, m := range models {
		got = append(got, idName{m.ID, m.Group.Name})
	}
	want := []idName{{0, "no_id"}, {7, "from_point"}, {20, "from_filename"}, {300, "explicit"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("models = %+v, want %+v", got, want)
	}
}

func TestParseDirEmpty(t *testing.T) {
	models, err := ParseDir(t.TempDir())
	if err != nil || models != nil {
		t.Errorf("ParseDir(empty dir) = %v, %v; want nil, nil", models, err)
	}
}

func TestParseDirErrors(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope")
		models, err := ParseDir(missing)
		if models != nil || !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ParseDir = %v, %v; want fs.ErrNotExist", models, err)
		}
		if err != nil && !strings.Contains(err.Error(), missing) {
			t.Errorf("error %q does not name the directory", err)
		}
	})

	t.Run("path is a file", func(t *testing.T) {
		dir := writeFiles(t, map[string]string{"plain": "x"})
		if _, err := ParseDir(filepath.Join(dir, "plain")); err == nil {
			t.Error("ParseDir on a regular file returned no error")
		}
	})

	malformed := map[string]string{
		"syntax error":           `{"group": `,
		"empty file":             ``,
		"wrong top-level type":   `[]`,
		"id is a string":         `{"id": "1", "group": {}}`,
		"size is a string":       `{"group": {"points": [{"name": "A", "size": "1"}]}}`,
		"sf is a float":          `{"group": {"points": [{"name": "A", "sf": 0.5}]}}`,
		"sf is an object":        `{"group": {"points": [{"name": "A", "sf": {}}]}}`,
		"count is a bool":        `{"group": {"groups": [{"name": "g", "count": true}]}}`,
		"symbol value is string": `{"group": {"points": [{"name": "A", "symbols": [{"name": "X", "value": "1"}]}]}}`,
		"points is an object":    `{"group": {"points": {}}}`,
	}
	for name, content := range malformed {
		t.Run(name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{
				"model_1.json": `{"group": {"name": "fine"}}`,
				"model_2.json": content,
			})
			models, err := ParseDir(dir)
			if err == nil {
				t.Fatalf("no error; got %+v", models)
			}
			if models != nil {
				t.Errorf("models = %+v, want nil alongside an error", models)
			}
			if !strings.Contains(err.Error(), filepath.Join(dir, "model_2.json")) {
				t.Errorf("error %q does not name the offending file", err)
			}
		})
	}

	t.Run("unreadable model file", func(t *testing.T) {
		// A directory named like a model file cannot be read as a file.
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "model_3.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := ParseDir(dir)
		if err == nil || !strings.Contains(err.Error(), "schema: read ") {
			t.Errorf("err = %v, want a read error", err)
		}
	})
}

func TestParseFileErrorsWrapCause(t *testing.T) {
	_, err := parseFile(filepath.Join(t.TempDir(), "model_1.json"), "model_1.json")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}

	dir := writeFiles(t, map[string]string{"model_1.json": `{"group": 5}`})
	_, err = parseFile(filepath.Join(dir, "model_1.json"), "model_1.json")
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want it to wrap *json.UnmarshalTypeError", err)
	}
}

// pointSizes are the register counts the SunSpec point types must have.
var pointSizes = map[string]int{
	"int16": 1, "uint16": 1, "count": 1, "acc16": 1, "enum16": 1, "bitfield16": 1, "pad": 1, "sunssf": 1,
	"int32": 2, "uint32": 2, "acc32": 2, "enum32": 2, "bitfield32": 2, "float32": 2, "ipaddr": 2,
	"int64": 4, "uint64": 4, "acc64": 4, "bitfield64": 4, "float64": 4, "eui48": 4,
	"ipv6addr": 8,
}

// TestParseRealModels parses the SunSpec model definitions shipped in the
// repository and checks the invariants the generator and decoder rely on.
func TestParseRealModels(t *testing.T) {
	dir := filepath.Join("..", "..", "models")
	models, err := ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir(%s): %v", dir, err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "model_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != len(files) || len(models) < 100 {
		t.Fatalf("parsed %d models from %d files, want one per file and at least 100", len(models), len(files))
	}
	if !sort.SliceIsSorted(models, func(i, j int) bool { return models[i].ID < models[j].ID }) {
		t.Error("models are not sorted by ID")
	}

	var checkGroup func(t *testing.T, modelID int, g GroupDef, top bool)
	checkGroup = func(t *testing.T, modelID int, g GroupDef, top bool) {
		t.Helper()
		if g.Name == "" {
			t.Errorf("model %d: group without a name", modelID)
		}
		if len(g.Points) == 0 && len(g.Groups) == 0 {
			t.Errorf("model %d group %s: neither points nor groups", modelID, g.Name)
		}
		seen := make(map[string]bool)
		for _, p := range g.Points {
			if p.Name == "" {
				t.Errorf("model %d group %s: point without a name", modelID, g.Name)
			}
			if seen[p.Name] {
				t.Errorf("model %d group %s: duplicate point %s", modelID, g.Name, p.Name)
			}
			seen[p.Name] = true
			switch want, known := pointSizes[p.Type]; {
			case p.Type == "string":
				if p.Size < 1 {
					t.Errorf("model %d point %s: string with size %d", modelID, p.Name, p.Size)
				}
			case !known:
				t.Errorf("model %d point %s: unknown type %q", modelID, p.Name, p.Type)
			case p.Size != want:
				t.Errorf("model %d point %s: %s with size %d, want %d", modelID, p.Name, p.Type, p.Size, want)
			}
			if p.Access != "" && p.Access != "R" && p.Access != "RW" {
				t.Errorf("model %d point %s: access %q", modelID, p.Name, p.Access)
			}
			if p.Mandatory != "" && p.Mandatory != "M" && p.Mandatory != "O" {
				t.Errorf("model %d point %s: mandatory %q", modelID, p.Name, p.Mandatory)
			}
			if p.Static != "" && p.Static != "S" && p.Static != "D" {
				t.Errorf("model %d point %s: static %q", modelID, p.Name, p.Static)
			}
			if p.SF.IsSet && !p.SF.IsLiteral && p.SF.Ref == "" {
				t.Errorf("model %d point %s: empty scale factor reference", modelID, p.Name)
			}
			if p.SF.IsSet && p.SF.IsLiteral && (p.SF.IntVal < -10 || p.SF.IntVal > 10) {
				t.Errorf("model %d point %s: literal scale factor %d out of range", modelID, p.Name, p.SF.IntVal)
			}
			if len(p.Symbols) > 0 && !strings.HasPrefix(p.Type, "enum") && !strings.HasPrefix(p.Type, "bitfield") {
				t.Errorf("model %d point %s: symbols on a %s point", modelID, p.Name, p.Type)
			}
			symSeen := make(map[string]bool)
			for _, s := range p.Symbols {
				if s.Name == "" || s.Value < 0 || symSeen[s.Name] {
					t.Errorf("model %d point %s: bad or duplicate symbol %+v", modelID, p.Name, s)
				}
				symSeen[s.Name] = true
			}
		}
		if top {
			if len(g.Points) < 2 || g.Points[0].Name != "ID" || g.Points[1].Name != "L" {
				t.Errorf("model %d: top-level group does not start with ID and L", modelID)
			} else if v, ok := g.Points[0].Value.(float64); !ok || int(v) != modelID {
				t.Errorf("model %d: ID point has value %v", modelID, g.Points[0].Value)
			}
		}
		for _, sub := range g.Groups {
			if sub.Count.IsString && sub.Count.StringVal == "" {
				t.Errorf("model %d group %s: empty count reference", modelID, sub.Name)
			}
			if !sub.Count.IsString && sub.Count.IntVal < 0 {
				t.Errorf("model %d group %s: negative count %d", modelID, sub.Name, sub.Count.IntVal)
			}
			checkGroup(t, modelID, sub, false)
		}
	}

	ids := make(map[int]bool)
	for _, m := range models {
		if m.ID < 1 || m.ID > 65534 {
			t.Errorf("model %q: ID %d is not a valid SunSpec model ID", m.Group.Name, m.ID)
		}
		if ids[m.ID] {
			t.Errorf("model ID %d defined twice", m.ID)
		}
		ids[m.ID] = true
		checkGroup(t, m.ID, m.Group, true)
	}

	// The file name, where it carries a number, agrees with the model ID.
	for _, f := range files {
		name := filepath.Base(f)
		if n := idFromFilename(name); n != 0 && !ids[n] {
			t.Errorf("%s: no model with ID %d was parsed", name, n)
		}
	}
	for _, id := range []int{1, 101, 103, 160} {
		if !ids[id] {
			t.Errorf("model %d is missing from %s", id, dir)
		}
	}
}
