// SPDX-License-Identifier: MIT

package devicefile

import (
	"bytes"
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

// TestFileAllocationBudget: Parse and File.Server of a file at MaxFileSize
// allocate a bounded multiple of the file's size (LIMITATIONS.md). The file
// holds as many empty group instances as fit, which costs the most memory
// per byte; the conversion stops after 65535 of them.
//
// Under the race detector, parsing that file takes ten times as long, so the
// file is an eighth of MaxFileSize there: the budget is per byte, and the
// conversion stops at the same instance. The runs without it, CI's among
// them, use the full size.
func TestFileAllocationBudget(t *testing.T) {
	size := MaxFileSize
	if raceEnabled {
		size /= 8
	}
	head, tail := `{"models": [{"id": 160, "groups": {"module": [`, `]}}]}`
	var b bytes.Buffer
	b.Grow(size)
	b.WriteString(head)
	for b.Len()+3+len(tail) <= size {
		if b.Len() > len(head) {
			b.WriteByte(',')
		}
		b.WriteString("{}")
	}
	b.WriteString(tail)
	data := b.Bytes()

	var parseErr, serverErr error
	parsed := allocated(func() {
		var file *File
		if file, parseErr = Parse(data); parseErr == nil {
			_, serverErr = file.Server(nil)
		}
	})
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	// Parse alone takes about 29 times the file's size for this shape; the
	// conversion adds next to nothing.
	const perByte = 40
	if limit := uint64(perByte * len(data)); parsed > limit {
		t.Errorf("Parse and File.Server of %d bytes allocated %d bytes (%.1f per byte), budget %d per byte",
			len(data), parsed, float64(parsed)/float64(len(data)), perByte)
	}
	var me *ModelError
	if !errors.As(serverErr, &me) || !errors.Is(serverErr, ErrInvalid) {
		t.Errorf("File.Server: %v, want the instance limit of ServerModels", serverErr)
	}
	t.Logf("%d bytes: %d allocated, %.1f per byte", len(data), parsed, float64(parsed)/float64(len(data)))
}
