// SPDX-License-Identifier: MIT

package sunspec

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// This file holds the value generator shared by the encoder round-trip tests
// and the end-to-end, chaos and stress tests: it fills any schema with random
// but valid values and knows what the decoder must make of them.

// genGroup is one generated group instance: the values to encode and what
// decoding them must yield.
type genGroup struct {
	vals *Values
	// raw is the expected DecodedPoint.RawValue of every point that is set.
	raw map[string]any
	// scaled is the expected ScaledValue of the points with a resolved scale
	// factor.
	scaled map[string]float64
	// children holds the instances of each nested group.
	children map[string][]*genGroup
	parent   *genGroup
	meta     *registry.GroupMeta
	counts   map[string]int
}

// sf returns the scale factor the decoder resolves for a reference.
func (g *genGroup) sf(ref string) (int16, bool) {
	if lit, err := strconv.Atoi(ref); err == nil {
		return int16(lit), true
	}
	for ; g != nil; g = g.parent {
		if pm := g.meta.Point(ref); pm != nil && pm.Type == "sunssf" {
			if v, ok := g.raw[ref]; ok {
				return v.(int16), true
			}
		}
	}
	return 0, false
}

func (g *genGroup) count(name string) int {
	for ; g != nil; g = g.parent {
		if n, ok := g.counts[name]; ok {
			return n
		}
	}
	return 0
}

// generator produces values for schemas.
type generator struct {
	rng *rand.Rand
	// maxInstances caps the instances generated for a repeating group.
	maxInstances int
	// sparse is the chance, in percent, that a point is left unset.
	sparse int
}

func newGenerator(seed int64) *generator {
	return &generator{rng: rand.New(rand.NewSource(seed)), maxInstances: 3, sparse: 15}
}

// model generates values for a whole model.
func (gen *generator) model(meta *registry.ModelMeta) *genGroup {
	return gen.group(meta.Group, nil)
}

func (gen *generator) group(gm *registry.GroupMeta, parent *genGroup) *genGroup {
	g := &genGroup{
		vals: &Values{}, raw: map[string]any{}, scaled: map[string]float64{},
		children: map[string][]*genGroup{}, parent: parent, meta: gm, counts: map[string]int{},
	}
	var derived []*registry.PointMeta
	// Scale factors first: the other points of the instance refer to them.
	for i := range gm.Points {
		pm := &gm.Points[i]
		if pm.Type != "sunssf" || countsGroups(gm, pm.Name) {
			continue
		}
		if gen.rng.Intn(100) < gen.sparse {
			continue
		}
		sf := int16(gen.rng.Intn(7) - 3)
		g.vals.Set(pm.Name, int(sf))
		g.raw[pm.Name] = sf
	}
	for i := range gm.Points {
		pm := &gm.Points[i]
		switch {
		case parent == nil && pm.Offset < 2, pm.Type == "sunssf" && !countsGroups(gm, pm.Name):
			continue
		case countsGroups(gm, pm.Name):
			n := gen.rng.Intn(gen.maxInstances + 1)
			g.counts[pm.Name] = n
			// Half of the time the count is left for the encoder to derive:
			// it then becomes the number of instances actually given.
			if gen.rng.Intn(2) == 0 {
				g.vals.Set(pm.Name, n)
				g.raw[pm.Name] = countRaw(pm.Type, n)
			} else {
				derived = append(derived, pm)
			}
			continue
		case gen.rng.Intn(100) < gen.sparse:
			continue
		}
		gen.point(g, pm)
	}
	for _, child := range gm.Groups {
		n := child.Count
		switch {
		case child.CountPoint != "":
			n = g.count(child.CountPoint)
		case child.Count == 0:
			n = gen.rng.Intn(gen.maxInstances + 1)
		}
		for i := 0; i < n; i++ {
			c := gen.group(child, g)
			g.children[child.Name] = append(g.children[child.Name], c)
			g.vals.Add(child.Name, c.vals)
		}
	}
	for _, pm := range derived {
		g.raw[pm.Name] = countRaw(pm.Type, maxInstances(gm, g.vals, pm.Name))
	}
	return g
}

func countRaw(pointType string, n int) any {
	switch pointType {
	case "int16", "sunssf":
		return int16(n)
	case "uint32", "acc32", "enum32", "bitfield32":
		return uint32(n)
	default:
		return uint16(n)
	}
}

// intIn returns a random integer in [lo, hi], biased towards the ends.
func (gen *generator) intIn(lo, hi int64) int64 {
	switch gen.rng.Intn(8) {
	case 0:
		return lo
	case 1:
		return hi
	}
	span := uint64(hi - lo)
	if span == math.MaxUint64 {
		return int64(gen.rng.Uint64())
	}
	return lo + int64(gen.rng.Uint64()%(span+1))
}

// point generates one point value and records what it decodes to.
func (gen *generator) point(g *genGroup, pm *registry.PointMeta) {
	name := pm.Name
	sf, scaled := int16(0), false
	if pm.SF != "" {
		sf, scaled = g.sf(pm.SF)
	}
	// setInt gives the raw integer r, as the engineering value when scaled.
	setInt := func(r int64, typed any) {
		g.raw[name] = typed
		if scaled {
			// The correctly rounded decimal r x 10^sf, computed without the
			// decoder's arithmetic.
			eng, err := strconv.ParseFloat(fmt.Sprintf("%de%d", r, sf), 64)
			if err != nil {
				panic(err)
			}
			g.scaled[name] = eng
			if sf != 0 {
				g.vals.Set(name, eng)
				return
			}
		}
		switch gen.rng.Intn(4) {
		case 0:
			g.vals.Set(name, typed)
		case 1:
			g.vals.Set(name, json.Number(strconv.FormatInt(r, 10)))
		case 2:
			if r > -(1<<53) && r < 1<<53 {
				g.vals.Set(name, float64(r))
				return
			}
			g.vals.Set(name, r)
		default:
			g.vals.Set(name, r)
		}
	}
	// A scaled 64-bit value must survive a float64.
	lim64 := int64(math.MaxInt64)
	if scaled && sf != 0 {
		lim64 = 1 << 50
	}

	switch pm.Type {
	case "int16":
		r := gen.intIn(-32767, 32767)
		setInt(r, int16(r))
	case "uint16", "count":
		r := gen.intIn(0, 0xFFFE)
		setInt(r, uint16(r))
	case "acc16":
		r := gen.intIn(1, 0xFFFF)
		setInt(r, uint16(r))
	case "pad":
		r := gen.intIn(0, 0xFFFF)
		setInt(r, uint16(r))
	case "int32":
		r := gen.intIn(-math.MaxInt32, math.MaxInt32)
		setInt(r, int32(r))
	case "uint32":
		r := gen.intIn(0, 0xFFFFFFFE)
		setInt(r, uint32(r))
	case "acc32":
		r := gen.intIn(1, 0xFFFFFFFF)
		setInt(r, uint32(r))
	case "int64":
		r := gen.intIn(-lim64, lim64)
		setInt(r, r)
	case "uint64", "acc64":
		r := gen.intIn(1, lim64)
		g.raw[name] = uint64(r)
		if scaled {
			eng, _ := strconv.ParseFloat(fmt.Sprintf("%de%d", r, sf), 64)
			g.scaled[name] = eng
			if sf != 0 {
				g.vals.Set(name, eng)
				break
			}
		}
		if !scaled && gen.rng.Intn(2) == 0 {
			// Beyond int64: only reachable as a uint64.
			u := uint64(r) + 1<<63 - 2
			g.raw[name] = u
			g.vals.Set(name, u)
			break
		}
		g.vals.Set(name, uint64(r))
	case "enum16", "enum32":
		var v uint32
		if len(pm.Symbols) > 0 && gen.rng.Intn(2) == 0 {
			s := pm.Symbols[gen.rng.Intn(len(pm.Symbols))]
			v = uint32(s.Value)
			// The first symbol of that name is the one the encoder finds.
			for _, o := range pm.Symbols {
				if o.Name == s.Name {
					v = uint32(o.Value)
					break
				}
			}
			g.vals.Set(name, s.Name)
		} else {
			v = uint32(gen.intIn(0, 0xFFFE))
			g.vals.Set(name, v)
		}
		if pm.Type == "enum16" {
			g.raw[name] = uint16(v)
		} else {
			g.raw[name] = v
		}
	case "bitfield16", "bitfield32", "bitfield64":
		bits := map[string]int{"bitfield16": 16, "bitfield32": 32, "bitfield64": 64}[pm.Type]
		var v uint64
		if len(pm.Symbols) > 0 && gen.rng.Intn(2) == 0 {
			var names []string
			for _, s := range pm.Symbols {
				if s.Value < bits && gen.rng.Intn(2) == 0 {
					names = append(names, s.Name)
				}
			}
			// The encoder sets the bit of the first symbol of each name.
			for _, n := range names {
				for _, s := range pm.Symbols {
					if s.Name == n {
						v |= 1 << uint(s.Value)
						break
					}
				}
			}
			if names == nil {
				names = []string{}
			}
			g.vals.Set(name, names)
		} else {
			v = gen.rng.Uint64() >> uint(64-bits+1)
			g.vals.Set(name, v)
		}
		if v == 1<<uint(bits)-1 || (bits == 64 && v == math.MaxUint64) {
			v--
			g.vals.Set(name, v)
		}
		switch bits {
		case 16:
			g.raw[name] = uint16(v)
		case 32:
			g.raw[name] = uint32(v)
		default:
			g.raw[name] = v
		}
	case "float32":
		f := float32(gen.rng.NormFloat64() * 1000)
		g.raw[name] = f
		if gen.rng.Intn(2) == 0 {
			g.vals.Set(name, f)
		} else {
			g.vals.Set(name, float64(f))
		}
	case "float64":
		f := gen.rng.NormFloat64() * 1e6
		g.raw[name] = f
		g.vals.Set(name, f)
	case "string":
		n := 1 + gen.rng.Intn(pm.Size*2)
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('!' + gen.rng.Intn(94))
		}
		g.raw[name] = string(b)
		g.vals.Set(name, string(b))
	case "ipaddr":
		ip := net.IPv4(byte(1+gen.rng.Intn(254)), byte(gen.rng.Intn(256)), byte(gen.rng.Intn(256)), byte(gen.rng.Intn(256)))
		g.raw[name] = ip.String()
		g.vals.Set(name, ip.String())
	case "ipv6addr":
		ip := make(net.IP, 16)
		gen.rng.Read(ip)
		ip[0] = 0xfd
		g.raw[name] = ip.String()
		g.vals.Set(name, ip.String())
	case "eui48":
		mac := make(net.HardwareAddr, 6)
		gen.rng.Read(mac)
		mac[0] = 0x02
		g.raw[name] = mac.String()
		g.vals.Set(name, mac.String())
	default:
		panic("generator: unhandled point type " + pm.Type)
	}
}

// check compares a decoded group instance with what was generated. path names
// the instance in failures. It returns the number of points compared.
func (g *genGroup) check(t testing.TB, got *DecodedGroup, path string) int {
	t.Helper()
	if got == nil {
		t.Errorf("%s: not decoded", path)
		return 0
	}
	n := 0
	if len(got.Points) != len(g.meta.Points) {
		t.Errorf("%s: %d points decoded, schema has %d", path, len(got.Points), len(g.meta.Points))
		return 0
	}
	for i := range g.meta.Points {
		pm := &g.meta.Points[i]
		dp := &got.Points[i]
		if g.parent == nil && pm.Offset < 2 {
			continue
		}
		n++
		want, set := g.raw[pm.Name]
		if !set {
			if pm.Type == "pad" {
				if dp.RawValue != uint16(0x8000) {
					t.Errorf("%s.%s: unset pad decodes to %v, want 0x8000", path, pm.Name, dp.RawValue)
				}
			} else if dp.Implemented {
				t.Errorf("%s.%s (%s): unset point decodes as implemented: %v", path, pm.Name, pm.Type, dp.RawValue)
			}
			continue
		}
		if dp.RawValue != want {
			t.Errorf("%s.%s (%s): RawValue = %T(%v), want %T(%v) (given %T(%v))",
				path, pm.Name, pm.Type, dp.RawValue, dp.RawValue, want, want, g.vals.Points[pm.Name], g.vals.Points[pm.Name])
			continue
		}
		// A count of zero in an accumulator-typed count point reads as not
		// implemented; nothing in the registry does that.
		if !dp.Implemented {
			t.Errorf("%s.%s (%s): %v decodes as not implemented", path, pm.Name, pm.Type, want)
		}
		if eng, ok := g.scaled[pm.Name]; ok {
			if dp.ScaledValue == nil {
				t.Errorf("%s.%s: no ScaledValue, want %v", path, pm.Name, eng)
			} else if diff := math.Abs(*dp.ScaledValue - eng); diff > 1e-9*math.Abs(eng) {
				t.Errorf("%s.%s: ScaledValue = %v, want %v", path, pm.Name, *dp.ScaledValue, eng)
			}
		} else if dp.ScaledValue != nil {
			t.Errorf("%s.%s: unexpected ScaledValue %v", path, pm.Name, *dp.ScaledValue)
		}
	}
	total := 0
	for _, child := range g.meta.Groups {
		want := g.children[child.Name]
		insts := got.GroupsNamed(child.Name)
		total += len(insts)
		if len(insts) != len(want) {
			t.Errorf("%s/%s: %d instances decoded, want %d", path, child.Name, len(insts), len(want))
			continue
		}
		for i, c := range want {
			if insts[i].Index != i+1 {
				t.Errorf("%s/%s[%d]: Index = %d", path, child.Name, i+1, insts[i].Index)
			}
			n += c.check(t, insts[i], fmt.Sprintf("%s/%s[%d]", path, child.Name, i+1))
		}
	}
	if total != len(got.Groups) {
		t.Errorf("%s: %d nested instances decoded, %d belong to schema groups", path, len(got.Groups), total)
	}
	return n
}

// registryIDs returns every model ID of the registry in ascending order.
func registryIDs() []uint16 {
	var ids []uint16
	for id := range registry.All() {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// walkMeta calls fn for gm and every group nested inside it.
func walkMeta(gm *registry.GroupMeta, fn func(*registry.GroupMeta)) {
	fn(gm)
	for _, c := range gm.Groups {
		walkMeta(c, fn)
	}
}
