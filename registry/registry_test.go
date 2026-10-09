// SPDX-License-Identifier: MIT

package registry_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

func TestByIDCommonModel(t *testing.T) {
	m := registry.ByID(1)
	if m == nil {
		t.Fatal("ByID(1) returned nil, expected Common model")
	}
	if m.Name != "common" {
		t.Errorf("Name = %q, want %q", m.Name, "common")
	}
	if m.Label != "Common" {
		t.Errorf("Label = %q, want %q", m.Label, "Common")
	}
	if m.Group == nil {
		t.Fatal("Group is nil")
	}
	if m.Group.Length != 68 {
		t.Errorf("Group.Length = %d, want 68", m.Group.Length)
	}
	if len(m.Group.Groups) != 0 {
		t.Errorf("model 1 should have no nested groups, got %d", len(m.Group.Groups))
	}
	points := m.Group.Points
	names := make(map[string]bool)
	for _, p := range points {
		names[p.Name] = true
	}
	for _, want := range []string{"ID", "L", "Mn", "Md", "SN", "DA"} {
		if !names[want] {
			t.Errorf("missing expected point %q", want)
		}
	}
}

func TestByIDInverterModel(t *testing.T) {
	m := registry.ByID(101)
	if m == nil {
		t.Fatal("ByID(101) returned nil, expected inverter model")
	}
	if m.Name != "inverter_single_phase" {
		t.Errorf("Name = %q, want %q", m.Name, "inverter_single_phase")
	}
	if m.Group == nil {
		t.Fatal("Group is nil")
	}
	pointMap := make(map[string]registry.PointMeta)
	for _, p := range m.Group.Points {
		pointMap[p.Name] = p
	}
	a, ok := pointMap["A"]
	if !ok {
		t.Fatal("point A not found in model 101")
	}
	if a.SF != "A_SF" {
		t.Errorf("point A SF = %q, want %q", a.SF, "A_SF")
	}
	asf, ok := pointMap["A_SF"]
	if !ok {
		t.Fatal("point A_SF not found in model 101")
	}
	if asf.Type != "sunssf" {
		t.Errorf("A_SF type = %q, want %q", asf.Type, "sunssf")
	}
}

func TestKnownUnknown(t *testing.T) {
	if !registry.Known(1) {
		t.Error("Known(1) = false, want true")
	}
	if registry.Known(65535) {
		t.Error("Known(65535) = true, want false")
	}
}

func TestCountPositive(t *testing.T) {
	c := registry.Count()
	if c < 100 {
		t.Errorf("Count() = %d, want >= 100", c)
	}
}

func TestFixedLength(t *testing.T) {
	common := registry.ByID(1)
	if common == nil {
		t.Fatal("ByID(1) returned nil")
	}
	if got := common.FixedLength(); got != 68 {
		t.Errorf("model 1 FixedLength() = %d, want 68", got)
	}

	// Model 160 (MPPT): FixedLength covers the top-level points only, not
	// the nested module group.
	mppt := registry.ByID(160)
	if mppt == nil {
		t.Fatal("ByID(160) returned nil")
	}
	if got := mppt.FixedLength(); got != 10 {
		t.Errorf("model 160 FixedLength() = %d, want 10", got)
	}
	if module := mppt.Group.Group("module"); module == nil || module.Length != 20 {
		t.Errorf("model 160 module group = %+v, want one of 20 registers", module)
	}

	if got := (&registry.ModelMeta{}).FixedLength(); got != 0 {
		t.Errorf("model without group: FixedLength() = %d, want 0", got)
	}
	var none *registry.ModelMeta
	if got := none.FixedLength(); got != 0 {
		t.Errorf("nil model: FixedLength() = %d, want 0", got)
	}
}

func TestGroupMetaHelpers(t *testing.T) {
	pt := &registry.GroupMeta{Name: "Pt", CountPoint: "NPt", Length: 1, Points: []registry.PointMeta{{Name: "V", Type: "uint16", Size: 1}}}
	first := &registry.GroupMeta{Name: "Dup", Count: 1}
	second := &registry.GroupMeta{Name: "Dup", Count: 2}
	root := &registry.GroupMeta{
		Name: "root", Count: 1, Length: 2,
		Points: []registry.PointMeta{{Name: "ID", Type: "uint16", Size: 1}, {Name: "V", Type: "int16", Size: 1, Offset: 1}},
		Groups: []*registry.GroupMeta{first, pt, second},
	}

	if got := root.Group("Pt"); got != pt {
		t.Errorf("Group(Pt) = %+v, want the nested group itself", got)
	}
	if got := root.Group("Dup"); got != first {
		t.Errorf("Group(Dup) = %+v, want the first group of that name", got)
	}
	if got := root.Group("V"); got != nil {
		t.Errorf("Group(V) = %+v, want nil: V is a point", got)
	}
	if got := root.Group("root"); got != nil {
		t.Errorf("Group(root) = %+v, want nil: a group is not nested in itself", got)
	}
	if got := pt.Group("anything"); got != nil {
		t.Errorf("Group on a leaf = %+v, want nil", got)
	}

	if got := root.Point("V"); got != &root.Points[1] {
		t.Errorf("Point(V) = %+v, want a pointer to the group's own point", got)
	}
	if got := root.Point("V"); got == nil || got.Type != "int16" {
		t.Errorf("Point(V) = %+v, want the top-level int16 point, not the nested one", got)
	}
	if got := root.Point("Pt"); got != nil {
		t.Errorf("Point(Pt) = %+v, want nil: Pt is a group", got)
	}
	if got := root.Point("v"); got != nil {
		t.Errorf("Point(v) = %+v, want nil: names are case-sensitive", got)
	}

	repeating := map[*registry.GroupMeta]bool{
		root:                     false, // Count 1
		first:                    false,
		second:                   true, // fixed count other than 1
		pt:                       true, // counted by a point
		{Name: "fill", Count: 0}: true,
		{Name: "both", Count: 1, CountPoint: "N"}:     true,
		{Name: "zero value"}:                          true,
		{Name: "negative", Count: -1}:                 true,
		{Name: "explicit once", Count: 1, Length: 99}: false,
	}
	for g, want := range repeating {
		if got := g.Repeating(); got != want {
			t.Errorf("%s: Repeating() = %v, want %v", g.Name, got, want)
		}
	}

	var none *registry.GroupMeta
	if none.Repeating() || none.Group("x") != nil || none.Point("x") != nil {
		t.Error("nil GroupMeta: helpers must report nothing")
	}
}

// walkGroups calls fn for g and every group nested in it, with the chain of
// enclosing groups (outermost first).
func walkGroups(g *registry.GroupMeta, parents []*registry.GroupMeta, fn func(g *registry.GroupMeta, parents []*registry.GroupMeta)) {
	fn(g, parents)
	chain := append(append([]*registry.GroupMeta{}, parents...), g)
	for _, c := range g.Groups {
		walkGroups(c, chain, fn)
	}
}

func TestPointOffsetsSumToGroupLength(t *testing.T) {
	for id, m := range registry.All() {
		if m.Group == nil {
			continue // bare models registered by the tests below
		}
		walkGroups(m.Group, nil, func(g *registry.GroupMeta, _ []*registry.GroupMeta) {
			sum := 0
			for _, p := range g.Points {
				sum += p.Size
			}
			if sum != g.Length {
				t.Errorf("model %d group %q: point sizes sum to %d, group length is %d", id, g.Name, sum, g.Length)
			}
			if n := len(g.Points); n > 0 {
				if end := g.Points[n-1].Offset + g.Points[n-1].Size; end != g.Length {
					t.Errorf("model %d group %q: last point ends at %d, group length is %d", id, g.Name, end, g.Length)
				}
			}
		})
	}
}

func TestRegisterNilIsIgnored(t *testing.T) {
	before := registry.Count()
	registry.Register(nil)
	if got := registry.Count(); got != before {
		t.Errorf("Count() = %d after Register(nil), want %d", got, before)
	}
}

// nextTestModelID is the next unused model ID for tests that register one.
var nextTestModelID uint16 = 65100

func TestRegisterAddsAndReplaces(t *testing.T) {
	// Not a SunSpec-assigned model ID, and a fresh one on every run so the
	// test also passes with -count.
	id := nextTestModelID
	nextTestModelID++
	if registry.Known(id) || registry.ByID(id) != nil {
		t.Fatalf("model %d is unexpectedly registered", id)
	}
	before := registry.Count()

	first := &registry.ModelMeta{ID: id, Name: "first"}
	registry.Register(first)
	if !registry.Known(id) || registry.ByID(id) != first {
		t.Errorf("ByID(%d) = %+v, want the registered schema itself", id, registry.ByID(id))
	}
	if got := registry.Count(); got != before+1 {
		t.Errorf("Count() = %d, want %d", got, before+1)
	}

	second := &registry.ModelMeta{ID: id, Name: "second"}
	registry.Register(second)
	if registry.ByID(id) != second {
		t.Errorf("ByID(%d) = %+v, want the replacement", id, registry.ByID(id))
	}
	if got := registry.Count(); got != before+1 {
		t.Errorf("Count() = %d after replacing, want %d", got, before+1)
	}
}

func TestAllReturnsACopyOfTheIndex(t *testing.T) {
	all := registry.All()
	if len(all) != registry.Count() {
		t.Fatalf("len(All()) = %d, Count() = %d", len(all), registry.Count())
	}
	for id, m := range all {
		if m == nil || m.ID != id {
			t.Errorf("All()[%d] = %+v, want a schema with that ID", id, m)
		}
		if registry.ByID(id) != m {
			t.Errorf("All()[%d] is not the schema ByID returns", id)
		}
	}
	delete(all, 1)
	all[65000] = &registry.ModelMeta{ID: 65000}
	if !registry.Known(1) || registry.Known(65000) {
		t.Error("modifying the map returned by All changed the registry")
	}
}

// knownTypes are the SunSpec point types the decoder understands.
var knownTypes = map[string]int{
	"int16": 1, "uint16": 1, "count": 1, "acc16": 1, "enum16": 1, "bitfield16": 1, "pad": 1, "sunssf": 1,
	"int32": 2, "uint32": 2, "acc32": 2, "enum32": 2, "bitfield32": 2, "float32": 2, "ipaddr": 2,
	"int64": 4, "uint64": 4, "acc64": 4, "bitfield64": 4, "float64": 4, "eui48": 4,
	"ipv6addr": 8,
	"string":   0, // any size
}

func TestGeneratedSchemasAreWellFormed(t *testing.T) {
	for id, m := range registry.All() {
		if id > 65000 {
			continue // registered by the tests above
		}
		if m.Name == "" || m.Label == "" {
			t.Errorf("model %d: Name=%q Label=%q, want both set", id, m.Name, m.Label)
		}
		root := m.Group
		if root == nil {
			t.Errorf("model %d: no group", id)
			continue
		}
		if root.Name != m.Name {
			t.Errorf("model %d: top-level group is named %q, model %q", id, root.Name, m.Name)
		}
		if root.Count != 1 || root.CountPoint != "" || root.Repeating() {
			t.Errorf("model %d: top-level group has Count=%d CountPoint=%q, want exactly once", id, root.Count, root.CountPoint)
		}
		// Every model starts with its ID and L header points.
		if p := root.Points; len(p) < 2 || p[0].Name != "ID" || p[1].Name != "L" ||
			p[0].Type != "uint16" || p[1].Type != "uint16" {
			t.Errorf("model %d: top-level group does not start with uint16 ID and L points", id)
		}

		walkGroups(root, nil, func(g *registry.GroupMeta, parents []*registry.GroupMeta) {
			if g.Name == "" {
				t.Errorf("model %d: group without a name below %d parents", id, len(parents))
			}
			if g.Type != "group" && g.Type != "sync" {
				t.Errorf("model %d group %q: Type = %q", id, g.Name, g.Type)
			}
			if g.Count < 0 {
				t.Errorf("model %d group %q: Count = %d", id, g.Name, g.Count)
			}
			if g.CountPoint != "" && g.Count != 0 {
				t.Errorf("model %d group %q: both Count %d and CountPoint %q", id, g.Name, g.Count, g.CountPoint)
			}
			// A group that repeats until the registers run out can only be
			// the last thing in the model: nothing may follow it.
			if g.CountPoint == "" && g.Count == 0 {
				if len(parents) != 1 || parents[0].Groups[len(parents[0].Groups)-1] != g {
					t.Errorf("model %d group %q: count 0 on a group that is not the last top-level one", id, g.Name)
				}
			}
			// A count point must be a point of an enclosing group, of an
			// integer type, so that it is decoded before the group.
			if g.CountPoint != "" {
				var cp *registry.PointMeta
				for i := len(parents) - 1; i >= 0 && cp == nil; i-- {
					cp = parents[i].Point(g.CountPoint)
				}
				switch {
				case cp == nil:
					t.Errorf("model %d group %q: count point %q not found in any enclosing group", id, g.Name, g.CountPoint)
				case cp.Type != "uint16" && cp.Type != "count":
					t.Errorf("model %d group %q: count point %q has type %s", id, g.Name, g.CountPoint, cp.Type)
				}
			}
			if g.Length <= 0 || len(g.Points) == 0 {
				t.Errorf("model %d group %q: Length=%d with %d points", id, g.Name, g.Length, len(g.Points))
			}
			seenGroup := make(map[string]bool)
			for _, c := range g.Groups {
				if c == nil {
					t.Errorf("model %d group %q: nil nested group", id, g.Name)
					continue
				}
				if seenGroup[c.Name] {
					t.Errorf("model %d group %q: duplicate nested group %q", id, g.Name, c.Name)
				}
				seenGroup[c.Name] = true
			}

			next := 0
			seen := make(map[string]bool)
			for _, p := range g.Points {
				where := func() string { return "model " + itoa(int(id)) + " group " + g.Name + " point " + p.Name }
				if p.Name == "" {
					t.Errorf("model %d group %q: point without a name at offset %d", id, g.Name, p.Offset)
				}
				if seen[p.Name] {
					t.Errorf("%s: duplicate point name", where())
				}
				seen[p.Name] = true
				if seenGroup[p.Name] {
					t.Errorf("%s: a nested group has the same name", where())
				}
				if p.Offset != next {
					t.Errorf("%s: Offset = %d, want %d (points must be contiguous)", where(), p.Offset, next)
				}
				next = p.Offset + p.Size
				want, ok := knownTypes[p.Type]
				switch {
				case !ok:
					t.Errorf("%s: unknown type %q", where(), p.Type)
				case want == 0 && p.Size < 1:
					t.Errorf("%s: %s with Size %d", where(), p.Type, p.Size)
				case want != 0 && p.Size != want:
					t.Errorf("%s: %s has Size %d, want %d", where(), p.Type, p.Size, want)
				}
				if p.Access != "R" && p.Access != "RW" {
					t.Errorf("%s: Access = %q", where(), p.Access)
				}
				if p.SFIsLiteral {
					if p.SF != itoa(p.SFLiteral) {
						t.Errorf("%s: literal SF %q does not match SFLiteral %d", where(), p.SF, p.SFLiteral)
					}
				} else if p.SFLiteral != 0 {
					t.Errorf("%s: SFLiteral = %d without SFIsLiteral", where(), p.SFLiteral)
				}
				// A named scale factor must be a sunssf point of this group
				// or an enclosing one.
				if p.SF != "" && !p.SFIsLiteral {
					sf := g.Point(p.SF)
					for i := len(parents) - 1; i >= 0 && sf == nil; i-- {
						sf = parents[i].Point(p.SF)
					}
					switch {
					case sf == nil:
						t.Errorf("%s: scale factor %q not found in the group or its parents", where(), p.SF)
					case sf.Type != "sunssf":
						t.Errorf("%s: scale factor %q has type %s", where(), p.SF, sf.Type)
					}
				}
				for _, s := range p.Symbols {
					if s.Name == "" || s.Value < 0 {
						t.Errorf("%s: symbol %+v", where(), s)
					}
				}
			}
		})
	}
}

// The models whose nested structure the old flat registry could not express.
func TestGeneratedNestedModels(t *testing.T) {
	// Model 704: four sibling groups, each exactly once.
	ctl := registry.ByID(704)
	if ctl == nil || ctl.Group == nil {
		t.Fatal("model 704 missing")
	}
	var names []string
	for _, g := range ctl.Group.Groups {
		names = append(names, g.Name)
		if g.Type != "sync" || g.Count != 1 || g.CountPoint != "" || g.Repeating() || g.Length != 2 || len(g.Groups) != 0 {
			t.Errorf("model 704 group %s = type %q count %d countPoint %q length %d", g.Name, g.Type, g.Count, g.CountPoint, g.Length)
		}
		if g.Point("PF") == nil || g.Point("PF").SF != "PF_SF" || g.Point("Ext") == nil {
			t.Errorf("model 704 group %s: points = %+v", g.Name, g.Points)
		}
	}
	if got, want := strings.Join(names, " "), "PFWInj PFWInjRvrt PFWAbs PFWAbsRvrt"; got != want {
		t.Errorf("model 704 groups = %q, want %q", got, want)
	}
	if ctl.FixedLength() != 59 || ctl.Group.Point("PF_SF") == nil {
		t.Errorf("model 704: FixedLength = %d", ctl.FixedLength())
	}

	// Model 705: curves counted by NCrv, each with points counted by NPt.
	vv := registry.ByID(705)
	crv := vv.Group.Group("Crv")
	if len(vv.Group.Groups) != 1 || crv == nil || crv.CountPoint != "NCrv" || crv.Count != 0 || crv.Length != 10 || !crv.Repeating() {
		t.Fatalf("model 705 Crv = %+v", crv)
	}
	pt := crv.Group("Pt")
	if len(crv.Groups) != 1 || pt == nil || pt.CountPoint != "NPt" || pt.Length != 2 || len(pt.Groups) != 0 {
		t.Fatalf("model 705 Crv/Pt = %+v", pt)
	}
	if v, vr := pt.Point("V"), pt.Point("Var"); v == nil || v.SF != "V_SF" || v.Offset != 0 || vr == nil || vr.SF != "DeptRef_SF" || vr.Offset != 1 || vr.Type != "int16" {
		t.Errorf("model 705 Pt points = %+v", pt.Points)
	}
	if vv.Group.Point("NPt") == nil || vv.Group.Point("NCrv") == nil || crv.Point("NPt") != nil {
		t.Error("model 705: NPt and NCrv must be top-level points")
	}

	// Model 707: Crv -> MustTrip / MayTrip / MomCess -> Pt.
	trip := registry.ByID(707)
	crv = trip.Group.Group("Crv")
	if crv == nil || crv.CountPoint != "NCrvSet" || crv.Length != 1 || len(crv.Groups) != 3 {
		t.Fatalf("model 707 Crv = %+v", crv)
	}
	for i, name := range []string{"MustTrip", "MayTrip", "MomCess"} {
		g := crv.Groups[i]
		if g.Name != name || g.Count != 1 || g.CountPoint != "" || g.Length != 1 || g.Point("ActPt") == nil {
			t.Errorf("model 707 Crv group %d = %+v, want %s", i, g, name)
		}
		pt := g.Group("Pt")
		if len(g.Groups) != 1 || pt == nil || pt.CountPoint != "NPt" || pt.Length != 3 || pt.Point("Tms") == nil || pt.Point("Tms").Offset != 1 {
			t.Errorf("model 707 %s/Pt = %+v", name, pt)
		}
	}

	// Model 160: a legacy repeating group, as many as the model length holds.
	module := registry.ByID(160).Group.Group("module")
	if module == nil || module.Count != 0 || module.CountPoint != "" || !module.Repeating() || module.Length != 20 {
		t.Errorf("model 160 module = %+v", module)
	}
	// Model 803: strings counted by NStr.
	str := registry.ByID(803).Group.Group("string")
	if str == nil || str.CountPoint != "NStr" || str.Length != 32 {
		t.Errorf("model 803 string = %+v", str)
	}
}

// itoa formats an integer in decimal.
func itoa(n int) string {
	return strconv.Itoa(n)
}
