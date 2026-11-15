// SPDX-License-Identifier: MIT

package devicefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec"
)

// instancesFile returns a device file of model 705 with one curve holding n
// empty points, so that the model has n+1 group instances.
func instancesFile(n int) []byte {
	var b bytes.Buffer
	b.WriteString(`{"models": [{"id": 705, "groups": {"Crv": [{"groups": {"Pt": [`)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("{}")
	}
	b.WriteString(`]}}]}}]}`)
	return b.Bytes()
}

// TestServerModelsInstanceLimit: ServerModels converts at most 65535 group
// instances of one model, counted at every depth together, and refuses the
// next one itself, before the encoder sees the model.
func TestServerModelsInstanceLimit(t *testing.T) {
	for _, tc := range []struct {
		points int // instances of Pt; the model has one more (the curve)
		ok     bool
	}{
		{0, true},
		{1, true},
		{maxGroupInstances - 2, true},
		{maxGroupInstances - 1, true}, // 65535 instances: the limit
		{maxGroupInstances, false},    // 65536
		{maxGroupInstances + 1, false},
	} {
		t.Run(fmt.Sprint(tc.points+1), func(t *testing.T) {
			file, err := Parse(instancesFile(tc.points))
			if err != nil {
				t.Fatal(err)
			}
			models, err := file.ServerModels()
			if tc.ok {
				if err != nil {
					t.Fatalf("ServerModels: %v", err)
				}
				if got := len(models[0].Values.Groups["Crv"][0].Groups["Pt"]); got != tc.points {
					t.Fatalf("%d instances of Pt converted, want %d", got, tc.points)
				}
				return
			}
			var me *ModelError
			if !errors.As(err, &me) || !errors.Is(err, ErrInvalid) || !errors.Is(err, sunspec.ErrEncode) {
				t.Fatalf("ServerModels accepted %d group instances or refused them with %v, want a *ModelError matching ErrInvalid and sunspec.ErrEncode",
					tc.points+1, err)
			}
			want := fmt.Sprintf("models[0] (model 705): sunspec: encode model 705 group DERVoltVar/Crv[1]/Pt[%d]: more than 65535 group instances", maxGroupInstances)
			if !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("error %q, want it to start with %q", err, want)
			}
		})
	}
}

// TestServerModelsGroupsFollowTheSchema: a nested group that the schema
// does not have at that place is refused by ServerModels, at any depth, and
// the instances below it are not converted.
func TestServerModelsGroupsFollowTheSchema(t *testing.T) {
	deep := strings.Repeat(`{"groups": {"Crv": [`, 3300) + strings.Repeat(`]}}`, 3300)
	for name, tc := range map[string]struct {
		content string
		path    string
	}{
		"unknown at the top":             {`{"models": [{"id": 160, "groups": {"modules": [{}]}}]}`, "mppt"},
		"one deeper than the schema":     {`{"models": [{"id": 705, "groups": {"Crv": [{"groups": {"Pt": [{"groups": {"Pt": [{}]}}]}}]}}]}`, "DERVoltVar/Crv[1]/Pt[1]"},
		"a group repeated inside itself": {`{"models": [{"id": 705, "groups": {"Crv": [` + deep + `]}}]}`, "DERVoltVar/Crv[1]"},
	} {
		t.Run(name, func(t *testing.T) {
			file, err := Parse([]byte(tc.content))
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.ServerModels()
			var ee *sunspec.EncodeError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &ee) || ee.Group != tc.path || !strings.Contains(err.Error(), "the group has no nested group") {
				t.Fatalf("ServerModels: %v, want a *ModelError matching ErrInvalid for a group under %s", err, tc.path)
			}
		})
	}
}

// allocated returns the bytes fn allocates.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// fill returns a file of at most size bytes: head, as many elements as fit
// (elem(i) for the i-th), separated by commas, and tail.
func fill(size int, head string, elem func(i int) string, tail string) []byte {
	var b bytes.Buffer
	b.Grow(size)
	b.WriteString(head)
	for i := 0; ; i++ {
		e := elem(i)
		if b.Len()+1+len(e)+len(tail) > size {
			break
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(e)
	}
	b.WriteString(tail)
	return b.Bytes()
}

// longList returns a point value of n copies of el: the longest list a
// point value may be, for n = maxListLen.
func longList(n int, el string) string {
	return "[" + strings.TrimSuffix(strings.Repeat(el+",", n), ",") + "]"
}

// TestFileAllocationBudget: Parse and File.Server of a file at MaxFileSize
// allocate a bounded multiple of the file's size (LIMITATIONS.md), for the
// shapes that cost the most memory per byte: empty group instances (the
// conversion stops after 65535 of them), group instances and models with an
// empty "points" object, and point values that are lists as long as they
// may be, of numbers, strings and objects.
//
// Under the race detector, parsing takes ten times as long, so the files are
// an eighth of MaxFileSize there: the budget is per byte. The runs without
// it, CI's among them, use the full size.
func TestFileAllocationBudget(t *testing.T) {
	size := MaxFileSize
	if raceEnabled {
		size /= 8
	}
	numbers, strs, objects := longList(maxListLen, "0"), longList(maxListLen, `"a"`), longList(maxListLen, "{}")
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty instances", fill(size, `{"models": [{"id": 160, "groups": {"module": [`, func(int) string { return "{}" }, `]}}]}`)},
		{"instances with empty points", fill(size, `{"models": [{"id": 160, "groups": {"module": [`, func(int) string { return `{"points":{}}` }, `]}}]}`)},
		{"models with empty points", fill(size, `{"models": [`, func(int) string { return `{"id":1,"points":{}}` }, `]}`)},
		{"lists of numbers", fill(size, `{"models": [{"id": 1, "points": {`, func(i int) string { return fmt.Sprintf(`"p%d":%s`, i, numbers) }, `}}]}`)},
		{"lists of strings", fill(size, `{"models": [{"id": 1, "points": {`, func(i int) string { return fmt.Sprintf(`"p%d":%s`, i, strs) }, `}}]}`)},
		{"lists of objects", fill(size, `{"models": [{"id": 1, "points": {`, func(i int) string { return fmt.Sprintf(`"p%d":%s`, i, objects) }, `}}]}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var parseErr, serverErr error
			parsed := allocated(func() {
				var file *File
				if file, parseErr = Parse(tc.data); parseErr == nil {
					_, serverErr = file.Server(nil)
				}
			})
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			// Every shape is refused when it is built: too many instances or
			// models, points the model does not have.
			if serverErr == nil {
				t.Error("File.Server accepted the file")
			}
			// The costliest shape takes about 30 times the file's size.
			const perByte = 40
			if limit := uint64(perByte * len(tc.data)); parsed > limit {
				t.Errorf("Parse and File.Server of %d bytes allocated %d bytes (%.1f per byte), budget %d per byte",
					len(tc.data), parsed, float64(parsed)/float64(len(tc.data)), perByte)
			}
			t.Logf("%d bytes: %d allocated, %.1f per byte", len(tc.data), parsed, float64(parsed)/float64(len(tc.data)))
		})
	}
}

// TestPointValueListLimit: a list of more than 65535 values, which no point
// holds, is refused by Parse, at any depth of the value, and by the
// conversion of a group built in code; 65535 values are read.
func TestPointValueListLimit(t *testing.T) {
	file := func(value string) []byte {
		return []byte(`{"models": [{"id": 1, "points": {"Mn": ` + value + `}}]}`)
	}
	for name, tc := range map[string]struct {
		value string
		ok    bool
	}{
		"longest list":           {longList(maxListLen, "1"), true},
		"list too long":          {longList(maxListLen+1, "1"), false},
		"nested list too long":   {"[" + longList(maxListLen+1, `"a"`) + "]", false},
		"longest object":         {"{" + strings.TrimSuffix(strings.Repeat(`"k":1,`, maxListLen), ",") + "}", true},
		"object too large":       {"{" + strings.TrimSuffix(strings.Repeat(`"k":1,`, maxListLen+1), ",") + "}", false},
		"list in a large object": {`{"k": ` + longList(maxListLen+1, "1") + `}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			f, err := Parse(file(tc.value))
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if f != nil || !errors.Is(err, ErrSyntax) || !strings.Contains(err.Error(), "point Mn: ") || !strings.Contains(err.Error(), "65535") {
				t.Fatalf("Parse: %v, want a syntax error for point Mn", err)
			}
		})
	}

	long := make([]any, maxListLen+1)
	for i := range long {
		long[i] = json.Number("1")
	}
	g := &Group{Points: Points{{Name: "Mn", Value: long}}}
	if _, err := g.Values(); err == nil || !strings.Contains(err.Error(), "point Mn: a list of 65536 values") {
		t.Errorf("Values of a list of %d values: %v", len(long), err)
	}
	v, err := (&Group{Points: Points{{Name: "Mn", Value: long[:maxListLen]}}}).Values()
	if err != nil || len(v.Points["Mn"].([]uint16)) != maxListLen {
		t.Errorf("Values of a list of %d values: %v", maxListLen, err)
	}
}
