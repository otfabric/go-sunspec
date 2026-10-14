// SPDX-License-Identifier: MIT

package devicefile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"
	"github.com/otfabric/go-sunspec/testutil"
)

// exampleDevice is a hand-written device file using every way to give a
// value: engineering values with scale factors, symbol names, bitfield names,
// null, raw registers for a point, repeating groups and a vendor model.
const exampleDevice = `{
  "unit_id": 3,
  "base_address": 40000,
  "models": [
    {"id": 1, "name": "ignored", "points": {"Mn": "Acme", "Md": "PV-5000", "SN": "A-1", "DA": 3}},
    {"id": 103, "points": {
      "A": 12.34, "A_SF": -2,
      "PhVphA": 230.1, "V_SF": -1,
      "W": 5000, "W_SF": 1,
      "Hz": null,
      "WH": 123456789, "WH_SF": 0,
      "St": "MPPT",
      "Evt1": ["GROUND_FAULT", "OVER_TEMP"],
      "TmpCab": [65036], "Tmp_SF": -1
    }},
    {"id": 160,
      "points": {"DCA_SF": -1, "DCV_SF": 0, "DCW_SF": 0, "N": 2},
      "groups": {"module": [
        {"points": {"ID": 1, "IDStr": "east", "DCA": 8.5, "DCV": 300, "DCW": 2550}},
        {"points": {"ID": 2, "IDStr": "west", "DCA": 7.5, "DCV": 310}}
      ]}},
    {"id": 123, "points": {"WMaxLimPct": 100, "WMaxLimPct_SF": -1, "Conn": "CONNECT"}},
    {"id": 64900, "registers": [1, 2, 65535]}
  ]
}`

// mustServer parses a device file and builds its device.
func mustServer(t *testing.T, text string, opts *ServerOptions) *sunspec.Server {
	t.Helper()
	file, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv, err := file.Server(opts)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	return srv
}

func TestServeExampleDevice(t *testing.T) {
	srv := mustServer(t, exampleDevice, nil)
	if srv.UnitID() != 3 || srv.BaseAddress() != 40000 {
		t.Errorf("unit ID %d, base address %d", srv.UnitID(), srv.BaseAddress())
	}
	type layout struct{ id, start, length uint16 }
	var got []layout
	for _, h := range srv.Models() {
		got = append(got, layout{h.ID, h.StartAddress, h.Length})
	}
	want := []layout{{1, 40002, 66}, {103, 40070, 50}, {160, 40122, 48}, {123, 40172, 24}, {64900, 40198, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layout = %v, want %v", got, want)
	}

	// A client reads it like any device.
	client := connect(t, srv)
	ctx := testCtx(t)
	device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{UnitID: 3})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	models, err := device.ReadAll(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(models) != 5 {
		t.Fatalf("%d models read", len(models))
	}
	for _, dm := range models[:4] {
		if len(dm.Warnings) != 0 {
			t.Errorf("model %d: warnings %v", dm.ModelID, dm.Warnings)
		}
	}
	common, inv, mppt := models[0].Group, models[1].Group, models[2].Group
	if p := point(t, common, "Mn"); p.RawValue != "Acme" {
		t.Errorf("Mn = %v", p.RawValue)
	}
	if p := point(t, common, "Vr"); p.Implemented {
		t.Errorf("Vr = %+v, want not implemented", p)
	}
	scaled := func(g *sunspec.DecodedGroup, name string, want float64) {
		t.Helper()
		if p := point(t, g, name); p.ScaledValue == nil || *p.ScaledValue != want {
			t.Errorf("%s = %+v, want %v", name, p, want)
		}
	}
	scaled(inv, "A", 12.34)
	scaled(inv, "PhVphA", 230.1)
	scaled(inv, "W", 5000)
	scaled(inv, "WH", 123456789)
	scaled(inv, "TmpCab", -50)
	if p := point(t, inv, "W"); !reflect.DeepEqual(p.RawValue, int16(500)) {
		t.Errorf("W raw = %v (%T)", p.RawValue, p.RawValue)
	}
	if p := point(t, inv, "Hz"); p.Implemented {
		t.Errorf("Hz = %+v, want not implemented", p)
	}
	if p := point(t, inv, "St"); !reflect.DeepEqual(p.Symbols, []string{"MPPT"}) {
		t.Errorf("St = %+v", p)
	}
	if p := point(t, inv, "Evt1"); !reflect.DeepEqual(p.Symbols, []string{"GROUND_FAULT", "OVER_TEMP"}) {
		t.Errorf("Evt1 = %+v", p)
	}
	modules := mppt.GroupsNamed("module")
	if len(modules) != 2 {
		t.Fatalf("%d MPPT modules", len(modules))
	}
	scaled(modules[0], "DCA", 8.5)
	scaled(modules[1], "DCV", 310)
	if p := point(t, modules[1], "IDStr"); p.RawValue != "west" {
		t.Errorf("IDStr = %v", p.RawValue)
	}
	if p := point(t, modules[1], "DCW"); p.Implemented {
		t.Errorf("module 2 DCW = %+v, want not implemented", p)
	}
	if !reflect.DeepEqual(models[4].RawRegisters, []uint16{1, 2, 65535}) || models[4].Group != nil {
		t.Errorf("vendor model = %+v", models[4])
	}
	if p, err := device.ReadPoint(ctx, *device.ModelByID(123), "WMaxLimPct"); err != nil || p.ScaledValue == nil || *p.ScaledValue != 100 {
		t.Errorf("WMaxLimPct = %+v, %v", p, err)
	}
	// Another unit ID is not this device.
	if _, err := sunspec.Detect(ctx, client, nil); err == nil {
		t.Error("detect with unit 1 succeeded")
	}
}

func TestServerOptions(t *testing.T) {
	unitID, base := uint8(9), uint16(50000)
	var writes []*sunspec.ServerWrite
	srv := mustServer(t, exampleDevice, &ServerOptions{
		UnitID: &unitID, BaseAddress: &base,
		OnWrite: func(_ context.Context, w *sunspec.ServerWrite) error {
			writes = append(writes, w)
			return nil
		},
	})
	if srv.UnitID() != 9 || srv.BaseAddress() != 50000 || len(srv.Models()) != 5 {
		t.Errorf("unit ID %d, base address %d, %d models", srv.UnitID(), srv.BaseAddress(), len(srv.Models()))
	}
	if h := srv.Models()[1]; h.ID != 103 || h.StartAddress != 50070 || h.Length != 50 {
		t.Errorf("model 1 = %+v", h)
	}
	// An accepted write reaches the hook, decoded. WMaxLimPct is at offset 5
	// of the Immediate Controls model.
	ctx := context.Background()
	ctl := srv.Models()[3].StartAddress
	if _, err := srv.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 9, Addr: ctl + 5, Quantity: 1, IsWrite: true, Args: []uint16{505}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(writes) != 1 || writes[0].ModelID != 123 || len(writes[0].Points) != 1 || writes[0].Points[0].Name != "WMaxLimPct" {
		t.Errorf("writes = %+v", writes)
	}

	// The other options are passed on.
	srv = mustServer(t, exampleDevice, &ServerOptions{RegType: modbus.InputRegister, SilentToOtherUnits: true})
	if srv.RegType() != modbus.InputRegister || srv.UnitID() != 3 {
		t.Errorf("register type %v, unit ID %d", srv.RegType(), srv.UnitID())
	}
	if _, err := srv.HandleInputRegisters(ctx, &modbus.InputRegistersRequest{UnitID: 4, Addr: 40000, Quantity: 2}); !errors.Is(err, modbus.ErrNoResponse) {
		t.Errorf("request for another unit of a silent device: %v", err)
	}
	// An override of the unit ID to 0 is refused like a file that says 0.
	zero := uint8(0)
	file, _ := Parse([]byte(exampleDevice))
	if _, err := file.Server(&ServerOptions{UnitID: &zero}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unit ID 0: %v", err)
	}
}

func TestMinimalFiles(t *testing.T) {
	// A file with only the models: unit 1 at 40000.
	srv := mustServer(t, `{"models": [{"id": 1}]}`, nil)
	if srv.UnitID() != DefaultUnitID || srv.BaseAddress() != DefaultBaseAddress || len(srv.Models()) != 1 {
		t.Errorf("minimal file: unit ID %d, base address %d, %d models", srv.UnitID(), srv.BaseAddress(), len(srv.Models()))
	}
	// No models at all is a valid, empty device.
	for _, text := range []string{`{}`, `{"models": []}`, `{"models": null}`} {
		if srv := mustServer(t, text, nil); len(srv.Models()) != 0 {
			t.Errorf("%s: %d models", text, len(srv.Models()))
		}
	}
	// Null is "not given" for every member.
	srv = mustServer(t, `{"unit_id": null, "base_address": null, "models": [{"id": 1, "name": null, "points": null, "groups": null, "registers": null}]}`, nil)
	if srv.UnitID() != 1 || srv.BaseAddress() != 40000 || len(srv.Models()) != 1 {
		t.Errorf("nulls: unit ID %d, base address %d", srv.UnitID(), srv.BaseAddress())
	}
}

// fileErrors lists files that are refused, with what the error must say and
// what it must match: ErrSyntax from Parse, ErrInvalid, ErrEncode or
// ErrModelLayout from Server.
var fileErrors = []struct {
	name    string
	content string
	is      error
	want    []string
}{
	{"syntax error", "{\n  \"models\": [\n    {\"id\": 1,}\n  ]\n}", ErrSyntax, []string{"line 3, column 14", "invalid character"}},
	{"not JSON", "hello", ErrSyntax, []string{"line 1, column 1"}},
	{"empty file", "", ErrSyntax, []string{"the file ends before the JSON document does"}},
	{"truncated", `{"models": [{"id": 1}`, ErrSyntax, []string{"the file ends before the JSON document does"}},
	{"trailing data", `{"models": []} {}`, ErrSyntax, []string{"data after the end of the JSON document"}},
	{"unknown member", `{"models": [{"id": 1, "point": {}}]}`, ErrSyntax, []string{`unknown field "point"`}},
	{"unknown top-level member", `{"unit": 1}`, ErrSyntax, []string{`unknown field "unit"`}},
	{"unknown member of a group", `{"models": [{"id": 160, "groups": {"module": [{"registers": [1]}]}}]}`, ErrSyntax, []string{`unknown field "registers"`}},
	{"wrong type", "{\n\"unit_id\": \"one\"}", ErrSyntax, []string{"line 2, column", "unit_id"}},
	{"unit ID too large", `{"unit_id": 300}`, ErrSyntax, []string{"unit_id"}},
	{"unit ID zero", `{"unit_id": 0, "models": []}`, ErrInvalid, []string{"unit ID 0 is the Modbus broadcast address"}},
	{"register out of range", `{"models": [{"id": 64900, "registers": [70000]}]}`, ErrSyntax, []string{"registers", "uint16"}},
	{"missing id", `{"models": [{"id": 1}, {"points": {}}]}`, ErrInvalid, []string{`models[1]: "id" is missing`}},
	{"registers and points", `{"models": [{"id": 1, "registers": [1], "points": {"Mn": "x"}}]}`, ErrInvalid,
		[]string{`models[0] (model 1): give either "registers" or "points" and "groups", not both`}},
	{"registers and groups", `{"models": [{"id": 160, "registers": [1], "groups": {"module": []}}]}`, ErrInvalid, []string{"not both"}},
	{"unknown model", `{"models": [{"id": 1}, {"id": 64900, "points": {"A": 1}}]}`, ErrInvalid,
		[]string{`models[1] (model 64900): no schema is known for this model: give its data as "registers"`}},
	{"empty registers", `{"models": [{"id": 64900, "registers": []}]}`, sunspec.ErrModelLayout, []string{"64900", "data registers"}},
	{"unknown point", `{"models": [{"id": 1}, {"id": 103, "points": {"Watts": 1}}]}`, sunspec.ErrEncode,
		[]string{"model 1 of the chain", "model 103", "point Watts", "no point of that name"}},
	{"unknown group", `{"models": [{"id": 160, "groups": {"modules": [{}]}}]}`, sunspec.ErrEncode, []string{"model 160", `no nested group "modules"`}},
	{"point in a nested group", `{"models": [{"id": 160, "groups": {"module": [{}, {"points": {"DCW": true}}]}}]}`, ErrInvalid,
		[]string{"models[0] (model 160): group module[2]: point DCW: unsupported value true"}},
	{"value out of range", `{"models": [{"id": 103, "points": {"W": 400000, "W_SF": 0}}]}`, sunspec.ErrValueOutOfRange,
		[]string{"model 103", "point W", "type int16", "value out of range"}},
	{"scaled value out of range", `{"models": [{"id": 103, "points": {"A": 700, "A_SF": -2}}]}`, sunspec.ErrValueOutOfRange,
		[]string{"model 103", "point A", "70000 does not fit"}},
	{"nested value out of range", `{"models": [{"id": 160, "groups": {"module": [{"points": {"DCW": -1}}]}}]}`, sunspec.ErrEncode,
		[]string{"model 160", "module[1]", "point DCW", "negative"}},
	{"unknown symbol", `{"models": [{"id": 103, "points": {"St": "RUNNING"}}]}`, sunspec.ErrEncode, []string{"point St", `no symbol "RUNNING"`}},
	{"string too long", `{"models": [{"id": 1, "points": {"Vr": "0123456789abcdefg"}}]}`, sunspec.ErrEncode, []string{"point Vr", "17 bytes"}},
	{"number for a string", `{"models": [{"id": 1, "points": {"Mn": 5}}]}`, sunspec.ErrEncode, []string{"point Mn", "string"}},
	{"object value", `{"models": [{"id": 1, "points": {"Mn": {"a": 1}}}]}`, ErrInvalid, []string{"models[0] (model 1): point Mn: unsupported value", "not an object"}},
	{"mixed list", `{"models": [{"id": 103, "points": {"Evt1": ["GROUND_FAULT", 1]}}]}`, ErrInvalid, []string{"point Evt1", "not both"}},
	{"list of lists", `{"models": [{"id": 103, "points": {"Evt1": [[1]]}}]}`, ErrInvalid, []string{"point Evt1", "a list must hold"}},
	{"raw register out of range", `{"models": [{"id": 103, "points": {"W": [70000]}}]}`, ErrInvalid, []string{"point W", "register value 70000"}},
	{"raw register fraction", `{"models": [{"id": 103, "points": {"W": [1.5]}}]}`, ErrInvalid, []string{"point W", "register value 1.5"}},
	{"too many raw registers", `{"models": [{"id": 103, "points": {"W": [1, 2]}}]}`, sunspec.ErrEncode, []string{"point W", "2 raw registers"}},
	{"duplicate point", `{"models": [{"id": 103, "points": {"W": 1, "W": 2}}]}`, ErrSyntax, []string{"point W is given twice"}},
	{"points not an object", `{"models": [{"id": 103, "points": [1]}]}`, ErrSyntax, []string{`"points" must be an object`}},
	{"too many instances", `{"models": [{"id": 705, "points": {"NCrv": 1}, "groups": {"Crv": [{}, {}]}}]}`, sunspec.ErrEncode,
		[]string{"model 705", "point NCrv", "count is 1 but 2 instances are given"}},
	{"header given", `{"models": [{"id": 1, "points": {"ID": 1}}]}`, sunspec.ErrEncode, []string{"point ID", "set by the encoder"}},
	{"does not fit the address space", `{"base_address": 65500, "models": [{"id": 1}]}`, sunspec.ErrModelLayout, []string{"base address 65500", "invalid model layout"}},
}

// build parses a device file and builds its device, returning the first
// error.
func build(text string) error {
	file, err := Parse([]byte(text))
	if err != nil {
		return err
	}
	_, err = file.Server(nil)
	return err
}

func TestFileErrors(t *testing.T) {
	for _, tc := range fileErrors {
		err := build(tc.content)
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		if !errors.Is(err, tc.is) {
			t.Errorf("%s: error %q does not match %v", tc.name, err, tc.is)
		}
		// Only Parse reports syntax errors, and nothing else.
		_, parseErr := Parse([]byte(tc.content))
		var syntaxErr *SyntaxError
		if (parseErr != nil) != (tc.is == ErrSyntax) || (parseErr != nil && !errors.As(parseErr, &syntaxErr)) {
			t.Errorf("%s: Parse returned %v", tc.name, parseErr)
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q lacks %q", tc.name, err, want)
			}
		}
	}
}

// TestErrorMessages pins the complete text of the errors users see.
func TestErrorMessages(t *testing.T) {
	for content, want := range map[string]string{
		"{\n  \"models\": [\n    {\"id\": 1,}\n  ]\n}": "line 3, column 14: invalid character '}' looking for beginning of object key string",
		"":                      "the file ends before the JSON document does: EOF",
		`{"models": [{"id": 1}`: "the file ends before the JSON document does: unexpected EOF",
		`{"models": []} {}`:     "data after the end of the JSON document",
		`{"unit": 1}`:           `json: unknown field "unit"`,
		`{"models": [{"id": 103, "points": {"W": 1, "W": 2}}]}`:                                              "point W is given twice",
		`{"models": [{"id": 103, "points": [1]}]}`:                                                           `"points" must be an object of point names and values`,
		`{"unit_id": 0, "models": []}`:                                                                       "unit ID 0 is the Modbus broadcast address: use 1 to 255",
		`{"models": [{"id": 1}, {"points": {}}]}`:                                                            `models[1]: "id" is missing`,
		`{"models": [{"id": 1, "registers": [1], "points": {"Mn": "x"}}]}`:                                   `models[0] (model 1): give either "registers" or "points" and "groups", not both`,
		`{"models": [{"id": 64900, "points": {"A": 1}}]}`:                                                    `models[0] (model 64900): no schema is known for this model: give its data as "registers"`,
		`{"models": [{"id": 1, "points": {"Mn": true}}]}`:                                                    "models[0] (model 1): point Mn: unsupported value true: use a number, a string, a list or null",
		`{"models": [{"id": 1, "points": {"Mn": {}}}]}`:                                                      "models[0] (model 1): point Mn: unsupported value: use a number, a string, a list or null, not an object",
		`{"models": [{"id": 103, "points": {"Evt1": ["GROUND_FAULT", 1]}}]}`:                                 "models[0] (model 103): point Evt1: a list must hold either bitfield symbol names or raw register values, not both",
		`{"models": [{"id": 103, "points": {"Evt1": [[1]]}}]}`:                                               "models[0] (model 103): point Evt1: a list must hold bitfield symbol names or raw register values",
		`{"models": [{"id": 103, "points": {"W": [70000]}}]}`:                                                "models[0] (model 103): point W: register value 70000 is not a whole number from 0 to 65535",
		`{"models": [{"id": 705, "groups": {"Crv": [{"groups": {"Pt": [{}, {"points": {"V": false}}]}}]}}]}`: "models[0] (model 705): group Crv[1]: group Pt[2]: point V: unsupported value false: use a number, a string, a list or null",
	} {
		if err := build(content); err == nil || err.Error() != want {
			t.Errorf("%s:\n got  %v\n want %s", content, err, want)
		}
	}
}

func TestErrorTypes(t *testing.T) {
	_, err := Parse([]byte("{\n\"unit_id\": \"one\"}"))
	var syntaxErr *SyntaxError
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &syntaxErr) || syntaxErr.Line != 2 || syntaxErr.Column < 1 || !errors.As(err, &typeErr) || !errors.Is(err, ErrSyntax) {
		t.Errorf("type error = %#v", err)
	}
	if _, err = Parse([]byte(`{"x": 1}`)); !errors.As(err, &syntaxErr) || syntaxErr.Line != 0 || syntaxErr.Column != 0 {
		t.Errorf("unknown member = %#v", err)
	}
	if _, err = Parse(nil); !errors.Is(err, ErrSyntax) || errors.Is(err, ErrInvalid) {
		t.Errorf("empty text = %v", err)
	}

	err = build(`{"models": [{"id": 1}, {"id": 103, "points": {"W": true}}]}`)
	var modelErr *ModelError
	if !errors.As(err, &modelErr) || modelErr.Index != 1 || modelErr.ModelID == nil || *modelErr.ModelID != 103 || !errors.Is(err, ErrInvalid) || errors.Is(err, ErrSyntax) {
		t.Errorf("model error = %#v", err)
	}
	if err = build(`{"models": [{}]}`); !errors.As(err, &modelErr) || modelErr.Index != 0 || modelErr.ModelID != nil || errors.Unwrap(err) == nil {
		t.Errorf("model without ID = %#v", err)
	}
	// A value that does not fit is the encoder's error, with its location.
	err = build(`{"models": [{"id": 103, "points": {"W": 400000, "W_SF": 0}}]}`)
	var encErr *sunspec.EncodeError
	if !errors.As(err, &encErr) || encErr.PointName != "W" || errors.Is(err, ErrInvalid) {
		t.Errorf("encode error = %#v", err)
	}

	// Nil and empty errors still format and unwrap.
	var nilSyntax *SyntaxError
	var nilModel *ModelError
	if nilSyntax.Error() != ErrSyntax.Error() || nilSyntax.Unwrap() != nil || nilModel.Error() != ErrInvalid.Error() || nilModel.Unwrap() != nil {
		t.Error("nil errors")
	}
	if got := (&SyntaxError{}).Error(); got != "syntax error" {
		t.Errorf("empty SyntaxError = %q", got)
	}
	if got := (&SyntaxError{Line: 2, Column: 3}).Error(); got != "line 2, column 3: syntax error" {
		t.Errorf("SyntaxError without cause = %q", got)
	}
	id := uint16(7)
	if got := (&ModelError{Index: 4, ModelID: &id}).Error(); got != "models[4] (model 7): invalid model" {
		t.Errorf("ModelError without cause = %q", got)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "device.json")
	if err := os.WriteFile(good, []byte(exampleDevice), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := Load(good)
	if err != nil || *file.UnitID != 3 || len(file.Models) != 5 {
		t.Fatalf("Load = %+v, %v", file, err)
	}
	// A parse error names the file and the position.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{\n  \"models\": [\n    {\"id\": 1,}\n  ]\n}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(bad)
	if err == nil || !strings.HasPrefix(err.Error(), bad+": line 3, column 14: invalid character") || !errors.Is(err, ErrSyntax) {
		t.Errorf("Load of a bad file: %v", err)
	}
	// A file that is not there is the operating system's error.
	_, err = Load(filepath.Join(dir, "missing.json"))
	if err == nil || !strings.HasPrefix(err.Error(), "open ") || !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrSyntax) {
		t.Errorf("Load of a missing file: %v", err)
	}
}

// capture reads the device behind handler and returns its device file and
// the text of it.
func capture(t *testing.T, device *sunspec.Device, raw bool) (*File, string) {
	t.Helper()
	file, err := Capture(testCtx(t), device, raw)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	text, err := file.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return file, string(text)
}

func TestCaptureServeReproducesTheDevice(t *testing.T) {
	devices := map[string]*testutil.SunSpecHandler{
		"inverter": inverter(t),
		"der":      derDevice(t),
		"odd":      oddDevice(t),
	}
	for name, handler := range devices {
		for _, raw := range []bool{false, true} {
			original := discover(t, handler, 1)
			want := readAll(t, original)

			file, text := capture(t, original, raw)
			if len(file.Models) != len(want) {
				t.Fatalf("%s: %d models captured, %d read", name, len(file.Models), len(want))
			}
			for i, m := range file.Models {
				if raw && m.Registers == nil {
					t.Errorf("%s: a raw capture has model %d (ID %d) as points", name, i, *m.ID)
				}
			}
			checkSchema(t, name+" capture", []byte(text))

			// The written file, served.
			copyDevice := discover(t, mustServer(t, text, nil), 1)
			got := readAll(t, copyDevice)
			if a, b := decodedOnly(t, want), decodedOnly(t, got); a != b {
				t.Errorf("%s (raw %v): the served capture decodes differently\noriginal:\n%s\nserved:\n%s", name, raw, a, b)
			}
			// Raw registers reproduce the device to the bit.
			if raw && asJSON(t, got) != asJSON(t, want) {
				t.Errorf("%s: a raw capture does not reproduce the registers", name)
			}
			// Capturing the copy gives the same file: the format is stable.
			if _, again := capture(t, copyDevice, raw); again != text {
				t.Errorf("%s (raw %v): capturing the served copy gives another file\nfirst:\n%s\nsecond:\n%s", name, raw, text, again)
			}
		}
	}
}

func TestCaptureFileContents(t *testing.T) {
	_, text := capture(t, discover(t, oddDevice(t), 1), false)
	file, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("the captured file does not parse: %v\n%s", err, text)
	}
	if *file.UnitID != 1 || *file.BaseAddress != 0 || len(file.Models) != 5 {
		t.Fatalf("file = %+v", file)
	}
	// Short, over-long and vendor models are raw; the others are points.
	for i, wantRaw := range []bool{true, true, true, false, false} {
		if m := file.Models[i]; (m.Registers != nil) != wantRaw {
			t.Errorf("model %d (ID %d): raw = %v, want %v", i, *m.ID, m.Registers != nil, wantRaw)
		}
	}
	if m := file.Models[0]; len(m.Registers) != 65 || m.Name != "Common" {
		t.Errorf("short model: %d registers, name %q", len(m.Registers), m.Name)
	}
	if m := file.Models[1]; len(m.Registers) != 53 || m.Registers[52] != 9 {
		t.Errorf("long model: %d registers", len(m.Registers))
	}
	if m := file.Models[2]; !reflect.DeepEqual(m.Registers, []uint16{0xDEAD, 0xBEEF, 0, 1}) || m.Name != "unknown_64900" {
		t.Errorf("vendor model = %+v", m)
	}
	// Points are listed in schema order, with engineering values.
	mppt := text[strings.Index(text, `"id": 160`):]
	if !strings.Contains(mppt, `"DCA_SF": -1`) || !strings.Contains(mppt, `"DCA": 8.5`) ||
		strings.Index(mppt, `"DCA_SF"`) > strings.Index(mppt, `"N"`) {
		t.Errorf("MPPT model:\n%s", mppt)
	}
	if !strings.Contains(text, `"Mn": "Spacey"`) {
		t.Errorf("space-padded string was not captured as a value:\n%s", text)
	}
	if !strings.HasPrefix(text, "{\n  \"unit_id\": 1,\n  \"base_address\": 0,\n  \"models\": [\n    {\n      \"id\": 1,\n      \"name\": \"Common\",\n      \"registers\": [\n") ||
		!strings.HasSuffix(text, "\n  ]\n}\n") {
		t.Errorf("form of the file:\n%s", text)
	}
}

func TestCaptureErrors(t *testing.T) {
	// A model that cannot be read completely is not captured in part.
	file, err := Capture(testCtx(t), discover(t, brokenInverter(t), 1), false)
	if file != nil || err == nil || !strings.HasPrefix(err.Error(), "read model 64950: sunspec: partial read") || !errors.Is(err, sunspec.ErrPartialRead) {
		t.Errorf("partial read: %+v, %v", file, err)
	}
	// A context that ended.
	device := discover(t, inverter(t), 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Capture(ctx, device, false); !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "read model 1: ") {
		t.Errorf("cancelled context: %v", err)
	}
	if _, err := Capture(context.Background(), nil, false); !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("nil device: %v", err)
	}
	if _, err := Capture(context.Background(), &sunspec.Device{}, false); !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("device without discovery: %v", err)
	}
}

func TestFromDecodedFallsBackToRegisters(t *testing.T) {
	// Model 101 with a current of 12.34 A.
	regs := newModel(t, 101, 0).set("A", 1234).set("A_SF", neg2).fixture().Registers
	dm, err := sunspec.DecodeModel(append([]uint16{101, 50}, regs...), registry.ByID(101), 40070)
	if err != nil {
		t.Fatal(err)
	}
	m := FromDecoded(dm, false)
	if m.Registers != nil || len(m.Points) != 2 || m.Points[0].Name != "A" || m.Points[0].Value != 12.34 || *m.ID != 101 {
		t.Fatalf("clean model captured as %+v", m)
	}
	if m := FromDecoded(dm, true); m.Points != nil || !reflect.DeepEqual(m.Registers, regs) || *m.ID != 101 || m.Name != dm.Name {
		t.Errorf("raw capture = %+v", m)
	}
	// Whatever keeps the written model from reading back as the device's is
	// noticed, and the model is then captured as registers.
	for name, change := range map[string]func(*Model){
		"value that is not JSON":   func(m *Model) { m.Points[0].Value = make(chan int) },
		"value the file rejects":   func(m *Model) { m.Points[0].Value = true },
		"value the schema rejects": func(m *Model) { m.Points[0].Value = "text" },
		"point the schema lacks":   func(m *Model) { m.Points = append(m.Points, Point{Name: "Nope", Value: 1}) },
		"another value":            func(m *Model) { m.Points[0].Value = 6 },
	} {
		m := FromDecoded(dm, false)
		change(&m)
		if reproduces(&m, dm) {
			t.Errorf("%s: the changed model still counts as a reproduction", name)
		}
	}
	// A float that JSON cannot hold: the model is captured as registers.
	inf := sunspec.DecodedModel{ModelID: 101, Schema: dm.Schema, RawRegisters: dm.RawRegisters, Group: &sunspec.DecodedGroup{
		Points: []sunspec.DecodedPoint{{Name: "A", Type: "uint16", RawValue: math.Inf(1), Implemented: true}},
	}}
	if m := FromDecoded(&inf, false); len(m.Registers) != 50 || m.Points != nil {
		t.Errorf("model with an infinite value captured as %+v", m)
	}
	// A model with decoded points and no schema cannot be encoded again.
	orphan := sunspec.DecodedModel{ModelID: 64900, RawRegisters: []uint16{1, 2}, Group: &sunspec.DecodedGroup{}}
	if m := FromDecoded(&orphan, false); !reflect.DeepEqual(m.Registers, []uint16{1, 2}) || m.Points != nil {
		t.Errorf("model without schema captured as %+v", m)
	}
	if m := FromDecoded(nil, false); m.ID != nil || m.Registers != nil {
		t.Errorf("nil model captured as %+v", m)
	}
}

func TestTemplate(t *testing.T) {
	file := New(DefaultUnitID, DefaultBaseAddress)
	for _, id := range []uint16{1, 160, 705} {
		file.Models = append(file.Models, Template(registry.ByID(id)))
	}
	data, err := file.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	file, err = Parse(data)
	if err != nil {
		t.Fatalf("the template does not parse: %v\n%s", err, text)
	}
	if *file.UnitID != 1 || *file.BaseAddress != 40000 || len(file.Models) != 3 {
		t.Fatalf("file = %+v", file)
	}
	common := file.Models[0]
	if common.Name != "Common" || len(common.Points) != 6 || common.Points[0].Name != "Mn" || common.Points[5].Name != "DA" || common.Points[0].Value != nil {
		t.Errorf("Common template = %+v", common)
	}
	if !strings.Contains(text, `"Mn": null`) || strings.Contains(text, `"Pad"`) || strings.Contains(text, `"L"`) {
		t.Errorf("template text:\n%s", text)
	}
	if mods := file.Models[1].Groups["module"]; len(mods) != 1 || mods[0].Points[0].Name != "ID" {
		t.Errorf("MPPT template groups = %+v", file.Models[1].Groups)
	}
	if pts := file.Models[2].Groups["Crv"][0].Groups["Pt"]; len(pts) != 1 || len(pts[0].Points) != 2 {
		t.Errorf("volt-var template = %+v", file.Models[2].Groups)
	}

	// The template is a valid device: everything not implemented.
	models := readAll(t, discover(t, mustServer(t, text, nil), 1))
	if len(models) != 3 || len(models[2].Group.GroupsNamed("Crv")) != 1 || point(t, models[0].Group, "Mn").Implemented {
		t.Errorf("served template: %d models", len(models))
	}

	if m := Template(nil); m.ID != nil || m.Points != nil {
		t.Errorf("Template(nil) = %+v", m)
	}
	if m := Template(&registry.ModelMeta{ID: 5}); m.ID != nil {
		t.Errorf("template of a model without group = %+v", m)
	}
}

// TestTemplateEveryModel builds the template of every model of the registry:
// each is a valid file and a device that can be served.
func TestTemplateEveryModel(t *testing.T) {
	for id, meta := range registry.All() {
		file := New(DefaultUnitID, DefaultBaseAddress)
		file.Models = append(file.Models, Template(meta))
		data, err := file.Marshal()
		if err != nil {
			t.Errorf("model %d: %v", id, err)
			continue
		}
		checkSchema(t, "template", data)
		parsed, err := Parse(data)
		if err != nil {
			t.Errorf("model %d: the template does not parse: %v", id, err)
			continue
		}
		srv, err := parsed.Server(nil)
		if err != nil || len(srv.Models()) != 1 || srv.Models()[0].ID != id {
			t.Errorf("model %d: the template is not a device: %v", id, err)
		}
	}
}

func TestPointsJSON(t *testing.T) {
	// Order is kept both ways; null points and a null object are accepted.
	var g Group
	if err := json.Unmarshal([]byte(`{"points": {"b": 1, "a": null, "c": "x"}}`), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Points) != 3 || g.Points[0].Name != "b" || g.Points[0].Value != json.Number("1") || g.Points[1].Value != nil || g.Points[2].Value != "x" {
		t.Errorf("points = %+v", g.Points)
	}
	text, err := json.Marshal(g)
	if err != nil || string(text) != `{"points":{"b":1,"a":null,"c":"x"}}` {
		t.Errorf("marshal = %s, %v", text, err)
	}
	if err := json.Unmarshal([]byte(`{"points": null}`), &g); err != nil || g.Points != nil {
		t.Errorf("null points: %v, %+v", err, g.Points)
	}
	if _, err := json.Marshal(Points{{Name: "x", Value: make(chan int)}}); err == nil {
		t.Error("an unmarshalable value was marshalled")
	}
	// Called directly with text that is not JSON.
	var l Points
	for _, bad := range []string{``, `{"a": }`, `{"a"`, `{x}`} {
		if err := l.UnmarshalJSON([]byte(bad)); err == nil {
			t.Errorf("UnmarshalJSON(%q) succeeded", bad)
		}
	}
	if line, col := position([]byte("ab\ncd"), 99); line != 2 || col != 3 {
		t.Errorf("position past the end = %d, %d", line, col)
	}
	if line, col := position([]byte("ab"), -1); line != 1 || col != 1 {
		t.Errorf("position before the start = %d, %d", line, col)
	}
	// A null group instance is an empty one.
	file, err := Parse([]byte(`{"models": [{"id": 160, "groups": {"module": [null, {}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if models, err := file.ServerModels(); err != nil || len(models[0].Values.Groups["module"]) != 2 {
		t.Errorf("null instance: %v", err)
	}
}

func TestGroupValues(t *testing.T) {
	file, err := Parse([]byte(exampleDevice))
	if err != nil {
		t.Fatal(err)
	}
	v, err := file.Models[2].Values()
	if err != nil || len(v.Groups["module"]) != 2 || v.Points["N"] != json.Number("2") || v.Groups["module"][1].Points["IDStr"] != "west" {
		t.Errorf("Values = %+v, %v", v, err)
	}
	// Lists become symbol names or registers.
	v, err = file.Models[1].Values()
	if err != nil || !reflect.DeepEqual(v.Points["Evt1"], []string{"GROUND_FAULT", "OVER_TEMP"}) || !reflect.DeepEqual(v.Points["TmpCab"], []uint16{65036}) {
		t.Errorf("Values = %+v, %v", v, err)
	}
	if hz, ok := v.Points["Hz"]; !ok || hz != nil {
		t.Errorf("null point = %v, %v", hz, ok)
	}
	v, err = (&Group{Points: Points{{Name: "Evt1", Value: []interface{}{}}}}).Values()
	if err != nil || !reflect.DeepEqual(v.Points["Evt1"], []string{}) {
		t.Errorf("empty list = %+v, %v", v, err)
	}
	if v, err := (*Group)(nil).Values(); err != nil || v == nil || len(v.Points) != 0 {
		t.Errorf("nil group = %+v, %v", v, err)
	}
	if _, err := (&Group{Points: Points{{Name: "W", Value: 1.5}}}).Values(); err == nil || err.Error() != "point W: unsupported value: use a number, a string, a list or null, not an object" {
		t.Errorf("a value that did not come from JSON: %v", err)
	}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWrite(t *testing.T) {
	file, err := Parse([]byte(exampleDevice))
	if err != nil {
		t.Fatal(err)
	}
	want, err := file.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := file.Write(&buf); err != nil || !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("Write: %v\n%s", err, buf.Bytes())
	}
	path := filepath.Join(t.TempDir(), "device.json")
	if err := file.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, want) {
		t.Errorf("WriteFile wrote %s (%v)", data, err)
	}
	// What is written is what was read: the same device file again.
	again, err := Load(path)
	if err != nil || !reflect.DeepEqual(again, file) {
		t.Errorf("the written file reads back as %+v (%v)", again, err)
	}

	// An empty file has its models as an empty list.
	if data, err := New(2, 0).Marshal(); err != nil || string(data) != "{\n  \"unit_id\": 2,\n  \"base_address\": 0,\n  \"models\": []\n}\n" {
		t.Errorf("empty file = %q, %v", data, err)
	}
	if data, err := (&File{}).Marshal(); err != nil || string(data) != "{\n  \"models\": null\n}\n" {
		t.Errorf("zero file = %q, %v", data, err)
	}

	// Failures: a value that is not JSON, a writer or a path that fails.
	broken := &File{Models: []Model{{Group: Group{Points: Points{{Name: "x", Value: make(chan int)}}}}}}
	buf.Reset()
	if err := broken.Write(&buf); err == nil || buf.Len() != 0 {
		t.Errorf("a file with an unmarshalable value was written: %v, %q", err, buf.String())
	}
	if err := broken.WriteFile(path); err == nil {
		t.Error("a file with an unmarshalable value was written to disk")
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, want) {
		t.Errorf("a failed WriteFile changed the file (%v)", err)
	}
	if err := file.Write(failingWriter{}); err == nil || err.Error() != "disk full" {
		t.Errorf("a failing writer went unnoticed: %v", err)
	}
	if err := file.WriteFile(filepath.Join(t.TempDir(), "no", "such", "dir", "x.json")); err == nil {
		t.Error("a file in a missing directory was written")
	}
}

// TestGolden pins the files this package writes to the bytes sunspecctl
// v0.1 wrote, before the format moved here: testdata holds the output of its
// "template" and of its "capture" of the devices "serve" ran from
// example.json and der.json.
func TestGolden(t *testing.T) {
	golden := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	file := New(DefaultUnitID, DefaultBaseAddress)
	for _, id := range []uint16{1, 160, 705} {
		file.Models = append(file.Models, Template(registry.ByID(id)))
	}
	data, err := file.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if want := golden("template_1_160_705.json"); string(data) != want {
		t.Errorf("template:\n%s\nwant:\n%s", data, want)
	}

	for name, unitID := range map[string]uint8{"example": 3, "der": 1} {
		device := discover(t, mustServer(t, golden(name+".json"), nil), unitID)
		for suffix, raw := range map[string]bool{"_capture.json": false, "_capture_raw.json": true} {
			_, text := capture(t, device, raw)
			if want := golden(name + suffix); text != want {
				t.Errorf("%s%s:\n%s\nwant:\n%s", name, suffix, text, want)
			}
			checkSchema(t, name+suffix, []byte(text))
		}
	}
}
