//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"
)

// release is the otfabric/sunspec-interop release of the reference images,
// whose digests come from the manifest of that release. Keep them in step with
// the Interop workflow (TestWorkflowPin).
const (
	release         = "v0.3.0"
	pysunspec2Image = "ghcr.io/otfabric/sunspec-interop-pysunspec2:" + release +
		"@sha256:0f34e284b3110c3123449a6c7390f9bc7860a50a2e54adc08245c994514a7e18"
	cbsigmaImage = "ghcr.io/otfabric/sunspec-interop-cbsigma:" + release +
		"@sha256:77c23c3fd3a6253ea25173e55f83238b076070ee53c051f493228c207d344d42"
)

// references are the independent implementations go-sunspec is tested
// against by default; the environment variable of each overrides its image.
var references = []struct{ name, image, env string }{
	{"pysunspec2", pysunspec2Image, "SUNSPEC_INTEROP_PYSUNSPEC2_IMAGE"},
	{"cbsigma", cbsigmaImage, "SUNSPEC_INTEROP_CBSIGMA_IMAGE"},
}

const (
	requireEnv    = "SUNSPEC_REQUIRE_INTEROP"
	containerPort = 1502
	hostGateway   = "host.docker.internal"
	addHost       = hostGateway + ":host-gateway"
	relTolerance  = 1e-6
)

// otfabricImageEnv names a sunspec-interop-otfabric image: go-sunspec itself, as
// published by otfabric/sunspec-interop. It is not a reference: the adapter
// otfabric runs only when this names an image, and what it shows is
// compatibility between releases of this library, not independent
// verification. adaptersEnv restricts a run to a comma-separated subset.
const (
	otfabricImageEnv = "SUNSPEC_INTEROP_OTFABRIC_IMAGE"
	adaptersEnv      = "SUNSPEC_INTEROP_ADAPTERS"
)

var fixtures = []string{"baseline", "edge", "altbase", "der"}

// peer is one adapter image of sunspec-interop that go-sunspec is tested
// against.
type peer struct {
	name  string
	image string
}

// peers returns the references and, when SUNSPEC_INTEROP_OTFABRIC_IMAGE names
// one, otfabric.
func peers(t *testing.T) []peer {
	t.Helper()
	var all []peer
	for _, r := range references {
		p := peer{r.name, r.image}
		if v := os.Getenv(r.env); v != "" {
			p.image = v
		}
		all = append(all, p)
	}
	if v := os.Getenv(otfabricImageEnv); v != "" {
		all = append(all, peer{"otfabric", v})
	}
	only := os.Getenv(adaptersEnv)
	if only == "" {
		return all
	}
	var out []peer
	for _, p := range all {
		for want := range strings.SplitSeq(only, ",") {
			if strings.TrimSpace(want) == p.name {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s=%q selects no adapter (otfabric needs %s)", adaptersEnv, only, otfabricImageEnv)
	}
	return out
}

// forEachPeer runs fn as a subtest per peer.
func forEachPeer(t *testing.T, fn func(t *testing.T, a peer)) {
	t.Helper()
	requireDocker(t)
	for _, a := range peers(t) {
		t.Run(a.name, func(t *testing.T) { fn(t, a) })
	}
}

// clientOperations are the client operations the image declares.
func (a peer) clientOperations(t *testing.T) []string {
	t.Helper()
	out, stderr, err := docker("run", "--rm", a.image, "print-capabilities")
	if err != nil {
		t.Fatalf("print-capabilities: %v: %s", err, stderr)
	}
	var caps struct {
		ClientOperations []string `json:"clientOperations"`
	}
	decode(t, out, &caps)
	return caps.ClientOperations
}

// requireDocker skips without Docker, or fails when SUNSPEC_REQUIRE_INTEROP=1
// says this job must have it.
func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "version").Run(); err != nil {
		if os.Getenv(requireEnv) == "1" {
			t.Fatalf("%s=1 and Docker is not usable: %v", requireEnv, err)
		}
		t.Skipf("Docker is not usable (%s=1 makes this a failure): %v", requireEnv, err)
	}
}

func docker(args ...string) (string, string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// decode reads JSON with numbers intact: a 64-bit accumulator does not fit a
// float64.
func decode(t *testing.T, data string, into any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%v in %.400s", err, data)
	}
}

type fixture struct {
	Name        string `json:"name"`
	UnitID      uint8  `json:"unitId"`
	BaseAddress uint16 `json:"baseAddress"`
}

func loadFixture(t *testing.T, a peer, name string) fixture {
	t.Helper()
	out, stderr, err := docker("run", "--rm", a.image, "print-fixture", name)
	if err != nil {
		t.Fatalf("print-fixture %s: %v: %s", name, err, stderr)
	}
	var fx fixture
	decode(t, out, &fx)
	return fx
}

// peerModel is one model as the peer read it.
type peerModel struct {
	ID      uint16         `json:"id"`
	Address uint16         `json:"address"`
	Length  uint16         `json:"length"`
	Values  map[string]any `json:"values"`
	Scaled  map[string]any `json:"scaled"`
	Error   string         `json:"error"`
}

type peerScan struct {
	BaseAddress uint16      `json:"baseAddress"`
	Models      []peerModel `json:"models"`
}

// peerClient runs one client operation of the image against a device reachable
// from the container as host:port. It returns the exit code and the "result"
// of the document.
func peerClient(t *testing.T, a peer, into any, op, host string, port int, unit uint8, flags ...string) int {
	t.Helper()
	args := append([]string{"run", "--rm", "--add-host", addHost, a.image, "client", op,
		"--host", host, "--port", strconv.Itoa(port), "--unit-id", strconv.Itoa(int(unit))}, flags...)
	out, stderr, err := docker(args...)
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("docker: %v", err)
		}
		code = ee.ExitCode()
	}
	var doc struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("client %s printed nothing, exit %d: %s", op, code, stderr)
	}
	decode(t, out, &doc)
	if doc.OK && into != nil {
		decode(t, string(doc.Result), into)
	}
	if !doc.OK {
		t.Logf("client %s: exit %d: %s", op, code, doc.Error.Message)
	}
	return code
}

// startDevice runs the image's device on a fixture. It returns its address on
// the Docker network, where another container reaches it on containerPort, and
// its port on this machine.
func startDevice(t *testing.T, a peer, fixture string) (string, int) {
	t.Helper()
	out, stderr, err := docker("run", "-d", "--rm", "-p", "127.0.0.1::1502", a.image, "server", "--fixture", "/fixtures/"+fixture+"/fixture.json")
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, stderr)
	}
	id := strings.TrimSpace(out)
	t.Cleanup(func() {
		if _, stderr, err := docker("rm", "-f", id); err != nil {
			t.Logf("docker rm: %v: %s", err, stderr)
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, _, err := docker("exec", id, "test", "-f", "/run/sunspec-interop/ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			logs, errs, _ := docker("logs", id)
			t.Fatalf("device %s never became ready: %s%s", fixture, logs, errs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	ip, stderr, err := docker("inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", id)
	if err != nil {
		t.Fatalf("docker inspect: %v: %s", err, stderr)
	}
	out, stderr, err = docker("port", id, "1502/tcp")
	if err != nil {
		t.Fatalf("docker port: %v: %s", err, stderr)
	}
	first, _, _ := strings.Cut(out, "\n")
	_, p, err := net.SplitHostPort(strings.TrimSpace(first))
	if err != nil {
		t.Fatalf("docker port %q: %v", out, err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(ip), port
}

// startServer serves srv on every address, so that a container reaches it.
func startServer(t *testing.T, srv *sunspec.Server) int {
	t.Helper()
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: fmt.Sprintf("tcp://0.0.0.0:%d", port), MaxClients: 16, Logger: modbus.NopLogger()}, srv)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ms.Stop(); err != nil {
			t.Logf("server stop: %v", err)
		}
	})
	return port
}

func openDevice(t *testing.T, port int, unit uint8) []*sunspec.DecodedModel {
	t.Helper()
	client, err := modbus.New(modbus.Config{URL: fmt.Sprintf("tcp://127.0.0.1:%d", port), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := client.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	dev, err := sunspec.Open(ctx, client, &sunspec.DiscoverOptions{UnitID: unit})
	if err != nil {
		t.Fatal(err)
	}
	models, err := dev.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return models
}

// sameRaw compares a decoded raw value with the same value in JSON.
func sameRaw(goValue any, peer any) bool {
	switch p := peer.(type) {
	case string:
		s, ok := goValue.(string)
		return ok && strings.TrimRight(s, "\x00") == strings.TrimRight(p, "\x00")
	case json.Number:
		switch v := goValue.(type) {
		case float32:
			f, err := p.Float64()
			return err == nil && float64(v) == float64(float32(f))
		case float64:
			f, err := p.Float64()
			return err == nil && v == f
		}
		return fmt.Sprint(goValue) == p.String()
	}
	return false
}

func near(a, b float64) bool {
	return a == b || math.Abs(a-b) <= relTolerance*math.Max(math.Abs(a), math.Abs(b))
}

// compareGroup compares every point of a decoded group, and of its nested
// instances, with what the peer read.
func compareGroup(t *testing.T, where string, g *sunspec.DecodedGroup, raw, scaled map[string]any) {
	t.Helper()
	for i := range g.Points {
		pt := &g.Points[i]
		path := where + "." + pt.Name
		peer, ok := raw[pt.Name]
		if !ok && pt.Type == "pad" {
			// A pad carries no value; cbsigma leaves it out of the document.
			continue
		}
		if !ok {
			t.Errorf("%s: the peer has no such point", path)
			continue
		}
		if peer == nil {
			if pt.Implemented {
				t.Errorf("%s: read %v, the peer reads not implemented", path, pt.RawValue)
			}
			continue
		}
		if !pt.Implemented {
			t.Errorf("%s: not implemented, the peer reads %v", path, peer)
			continue
		}
		if !sameRaw(pt.RawValue, peer) {
			t.Errorf("%s: read %#v, the peer reads %v", path, pt.RawValue, peer)
		}
		if pt.ScaledValue != nil {
			n, ok := scaled[pt.Name].(json.Number)
			if !ok {
				t.Errorf("%s: scaled %v, the peer reads %v", path, *pt.ScaledValue, scaled[pt.Name])
				continue
			}
			if f, err := n.Float64(); err != nil || !near(*pt.ScaledValue, f) {
				t.Errorf("%s: scaled %v, the peer reads %v", path, *pt.ScaledValue, n)
			}
		}
	}
	seen := map[string]int{}
	for _, inst := range g.Groups {
		rawList, scaledList := instances(raw[inst.Name]), instances(scaled[inst.Name])
		i := seen[inst.Name]
		seen[inst.Name]++
		if i >= len(rawList) || i >= len(scaledList) {
			t.Errorf("%s.%s[%d]: the peer has %d instances", where, inst.Name, i, len(rawList))
			continue
		}
		compareGroup(t, fmt.Sprintf("%s.%s[%d]", where, inst.Name, i), inst, rawList[i].(map[string]any), scaledList[i].(map[string]any))
	}
}

// instances is the list of a group's instances in a scan document: a list for a
// repeating group, an object for a group that occurs once (contract 1.2).
func instances(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		return []any{x}
	}
	return nil
}

// compareDevice compares a whole device: the model chain, then every point.
func compareDevice(t *testing.T, models []*sunspec.DecodedModel, scan *peerScan) {
	t.Helper()
	if len(models) != len(scan.Models) {
		t.Fatalf("%d models, the peer reads %d", len(models), len(scan.Models))
	}
	points := 0
	for i, m := range models {
		p := &scan.Models[i]
		where := fmt.Sprintf("model %d", m.ModelID)
		if p.Error != "" {
			t.Errorf("%s: the peer: %s", where, p.Error)
		}
		if m.ModelID != p.ID || m.InstanceAddress != p.Address {
			t.Errorf("%s at %d: the peer reads model %d at %d", where, m.InstanceAddress, p.ID, p.Address)
			continue
		}
		if len(m.Warnings) > 0 {
			t.Errorf("%s: warnings: %v", where, m.Warnings)
		}
		if m.Group == nil {
			t.Errorf("%s: not decoded", where)
			continue
		}
		compareGroup(t, where, m.Group, p.Values, p.Scaled)
		points += len(m.Group.Points)
	}
	if points == 0 {
		t.Fatal("no point compared: this run proves nothing")
	}
	t.Logf("%d models compared", len(models))
}

// valuesFrom builds encoder input from what the peer read: the encoder takes
// scaled values, and a point the peer reads as not implemented is left out.
// Where the scaled value equals the raw one (no scale factor, or 0), the raw
// value is used: a peer may print a scaled 64-bit accumulator as a float64,
// which loses its last digits.
func valuesFrom(raw, scaled map[string]any, top bool) *sunspec.Values {
	v := &sunspec.Values{}
	for name, value := range scaled {
		if top && (name == "ID" || name == "L") {
			continue
		}
		switch x := value.(type) {
		case nil:
		case []any, map[string]any:
			rawInst := instances(raw[name])
			for i, inst := range instances(x) {
				var r map[string]any
				if i < len(rawInst) {
					r, _ = rawInst[i].(map[string]any)
				}
				v.Add(name, valuesFrom(r, inst.(map[string]any), false))
			}
		default:
			v.Set(name, exact(raw[name], x))
		}
	}
	return v
}

// exact returns the raw value when it is a number equal to the scaled one as a
// float64, and the scaled value otherwise.
func exact(raw, scaled any) any {
	r, ok := raw.(json.Number)
	s, ok2 := scaled.(json.Number)
	if !ok || !ok2 {
		return scaled
	}
	rf, err := r.Float64()
	sf, err2 := s.Float64()
	if err != nil || err2 != nil || rf != sf {
		return scaled
	}
	return r
}

// peerRegisters is the map the peer encodes for a fixture.
func peerRegisters(t *testing.T, a peer, name string) []uint16 {
	t.Helper()
	out, stderr, err := docker("run", "--rm", a.image, "print-registers", name)
	if err != nil {
		t.Fatalf("print-registers %s: %v: %s", name, err, stderr)
	}
	var doc struct {
		Registers []uint16 `json:"registers"`
	}
	decode(t, out, &doc)
	return doc.Registers
}

// servedFrom lays out a Server with what the peer reads from a fixture.
func servedFrom(t *testing.T, fx fixture, scan *peerScan) *sunspec.Server {
	t.Helper()
	models := make([]sunspec.ServerModel, len(scan.Models))
	for i := range scan.Models {
		models[i] = sunspec.ServerModel{ID: scan.Models[i].ID, Values: valuesFrom(scan.Models[i].Values, scan.Models[i].Scaled, true)}
	}
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{UnitID: fx.UnitID, BaseAddress: fx.BaseAddress}, models...)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// TestGoReadsPeerDevice reads a device the peer encoded through the public
// client path, and compares every point with what the peer's own client reads
// from the same device.
func TestGoReadsPeerDevice(t *testing.T) {
	forEachPeer(t, func(t *testing.T, a peer) {
		for _, name := range fixtures {
			t.Run(name, func(t *testing.T) {
				fx := loadFixture(t, a, name)
				ip, port := startDevice(t, a, name)
				var scan peerScan
				if code := peerClient(t, a, &scan, "scan", ip, containerPort, fx.UnitID); code != 0 {
					t.Fatalf("%s scan: exit %d", a.name, code)
				}
				if scan.BaseAddress != fx.BaseAddress {
					t.Fatalf("%s finds base address %d, fixture %d", a.name, scan.BaseAddress, fx.BaseAddress)
				}
				compareDevice(t, openDevice(t, port, fx.UnitID), &scan)
			})
		}
	})
}

// TestPeerReadsGoServer serves through Server the values the peer read, and
// compares the registers with what the peer encodes, then what the peer's
// client reads from the Server with what go-sunspec reads.
func TestPeerReadsGoServer(t *testing.T) {
	forEachPeer(t, func(t *testing.T, a peer) {
		for _, name := range fixtures {
			t.Run(name, func(t *testing.T) {
				fx := loadFixture(t, a, name)
				ip, _ := startDevice(t, a, name)
				var want peerScan
				if code := peerClient(t, a, &want, "scan", ip, containerPort, fx.UnitID); code != 0 {
					t.Fatalf("%s scan of its own device: exit %d", a.name, code)
				}
				srv := servedFrom(t, fx, &want)
				port := startServer(t, srv)
				var got peerScan
				if code := peerClient(t, a, &got, "scan", hostGateway, port, fx.UnitID); code != 0 {
					t.Fatalf("%s scan of Server: exit %d", a.name, code)
				}
				if got.BaseAddress != fx.BaseAddress {
					t.Fatalf("%s finds base address %d, fixture %d", a.name, got.BaseAddress, fx.BaseAddress)
				}
				compareDevice(t, openDevice(t, port, fx.UnitID), &got)
				compareRegisters(t, srv, peerRegisters(t, a, name))
			})
		}
	})
}

// compareRegisters compares the Server's image, model by model, with the map
// the peer encodes for the same values.
func compareRegisters(t *testing.T, srv *sunspec.Server, want []uint16) {
	t.Helper()
	i := 2 // the marker
	for n := range srv.Models() {
		m, err := srv.Model(n)
		if err != nil {
			t.Fatal(err)
		}
		regs := m.RawRegisters
		if i+len(regs) > len(want) {
			t.Fatalf("model %d: past the end of the peer's %d registers", m.ModelID, len(want))
		}
		for j, r := range regs {
			if want[i+j] != r {
				t.Errorf("model %d offset %d: %#04x, the peer encodes %#04x", m.ModelID, j, r, want[i+j])
			}
		}
		i += len(regs)
	}
	if i != len(want)-2 {
		t.Errorf("%d registers before the end model, the peer has %d", i, len(want)-2)
	}
}

// TestPeerWritesGoServer writes a read-write point with the peer's client,
// and is refused on a read-only point. A peer whose client does not write
// (otfabric before go-sunspec v0.10.0) is skipped. A peer whose client writes
// points inside groups (write-group) also writes a curve point and a control
// group point of the DER fixture, and is refused on a read-only curve flag.
func TestPeerWritesGoServer(t *testing.T) {
	forEachPeer(t, func(t *testing.T, a peer) {
		ops := a.clientOperations(t)
		if !slices.Contains(ops, "write") {
			t.Skipf("%s declares no client operation write", a.name)
		}
		peerWritesGoServer(t, a)
		t.Run("group", func(t *testing.T) {
			if !slices.Contains(ops, "write-group") {
				t.Skipf("%s declares no client operation write-group", a.name)
			}
			peerWritesGoServerGroups(t, a)
		})
	})
}

// groupPoint finds a point by a path of group names, each with its 0-based
// instance, under a decoded model.
func groupPoint(m *sunspec.DecodedModel, groups []string, index []int, point string) *sunspec.DecodedPoint {
	g := m.Group
	for i, name := range groups {
		var next *sunspec.DecodedGroup
		n := 0
		for _, inst := range g.Groups {
			if inst.Name != name {
				continue
			}
			if n == index[i] {
				next = inst
				break
			}
			n++
		}
		if next == nil {
			return nil
		}
		g = next
	}
	for i := range g.Points {
		if g.Points[i].Name == point {
			return &g.Points[i]
		}
	}
	return nil
}

func peerWritesGoServerGroups(t *testing.T, a peer) {
	fx := loadFixture(t, a, "der")
	ip, _ := startDevice(t, a, "der")
	var want peerScan
	if code := peerClient(t, a, &want, "scan", ip, containerPort, fx.UnitID); code != 0 {
		t.Fatalf("%s scan: exit %d", a.name, code)
	}
	srv := servedFrom(t, fx, &want)
	port := startServer(t, srv)

	writes := []struct {
		model  uint16
		path   string
		groups []string
		index  []int
		point  string
		value  int
	}{
		{704, "PFWInj.PF", []string{"PFWInj"}, []int{0}, "PF", 970},
		{705, "Crv[1].Pt[2].V", []string{"Crv", "Pt"}, []int{1, 2}, "V", 1090},
		{705, "Crv[1].ActPt", []string{"Crv"}, []int{1}, "ActPt", 4},
	}
	for _, w := range writes {
		var res struct {
			Value json.Number `json:"value"`
		}
		if code := peerClient(t, a, &res, "write-group", hostGateway, port, fx.UnitID, "--model", strconv.Itoa(int(w.model)), "--point", w.path, "--value", strconv.Itoa(w.value)); code != 0 {
			t.Errorf("write-group %s: exit %d", w.path, code)
			continue
		}
		if res.Value.String() != strconv.Itoa(w.value) {
			t.Errorf("%s: %s reads back %s", w.path, a.name, res.Value)
		}
	}
	models := openDevice(t, port, fx.UnitID)
	for _, w := range writes {
		for _, m := range models {
			if m.ModelID != w.model {
				continue
			}
			got := groupPoint(m, w.groups, w.index, w.point)
			if got == nil || !sameRaw(got.RawValue, json.Number(strconv.Itoa(w.value))) {
				t.Errorf("after write-group %s, go-sunspec reads %+v", w.path, got)
			}
		}
	}

	if code := peerClient(t, a, nil, "write-group", hostGateway, port, fx.UnitID, "--model", "705", "--point", "Crv[0].ReadOnly", "--value", "0"); code != 1 {
		t.Errorf("write-group of a read-only curve flag: exit %d, want 1", code)
	}
}

func peerWritesGoServer(t *testing.T, a peer) {
	const (
		model = uint16(123)
		point = "WMaxLimPct"
		value = 750
	)
	fx := loadFixture(t, a, "baseline")
	ip, _ := startDevice(t, a, "baseline")
	var want peerScan
	if code := peerClient(t, a, &want, "scan", ip, containerPort, fx.UnitID); code != 0 {
		t.Fatalf("%s scan: exit %d", a.name, code)
	}
	srv := servedFrom(t, fx, &want)
	port := startServer(t, srv)

	var res struct {
		Value json.Number `json:"value"`
	}
	if code := peerClient(t, a, &res, "write", hostGateway, port, fx.UnitID, "--model", strconv.Itoa(int(model)), "--point", point, "--value", strconv.Itoa(value)); code != 0 {
		t.Fatalf("write: exit %d", code)
	}
	if res.Value.String() != strconv.Itoa(value) {
		t.Errorf("%s reads back %s", a.name, res.Value)
	}
	var got *sunspec.DecodedPoint
	for _, m := range openDevice(t, port, fx.UnitID) {
		if m.ModelID != model {
			continue
		}
		for i := range m.Group.Points {
			if m.Group.Points[i].Name == point {
				got = &m.Group.Points[i]
			}
		}
	}
	if got == nil || !sameRaw(got.RawValue, json.Number(strconv.Itoa(value))) {
		t.Fatalf("after the write, go-sunspec reads %+v", got)
	}

	if code := peerClient(t, a, nil, "write", hostGateway, port, fx.UnitID, "--model", "103", "--point", "W", "--value", "1"); code != 1 {
		t.Errorf("write of a read-only point: exit %d, want 1", code)
	}
}

// TestGoWritesPeerDevice writes read-write points of the peer's device with
// WritePoint, a scaled one and an enum, and the peer's own client reads them
// back. A read-only point is refused before anything is sent.
func TestGoWritesPeerDevice(t *testing.T) {
	forEachPeer(t, func(t *testing.T, a peer) {
		fx := loadFixture(t, a, "baseline")
		ip, port := startDevice(t, a, "baseline")
		client, err := modbus.New(modbus.Config{URL: fmt.Sprintf("tcp://127.0.0.1:%d", port), Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Open(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		dev, err := sunspec.Open(ctx, client, &sunspec.DiscoverOptions{UnitID: fx.UnitID})
		if err != nil {
			t.Fatal(err)
		}
		controls := dev.ModelByID(123)
		if controls == nil {
			t.Fatal("no model 123")
		}
		// WMaxLimPct_SF is -1 in the fixture: 62.5 % is 625.
		p, err := dev.WritePoint(ctx, *controls, "WMaxLimPct", 62.5)
		if err != nil {
			t.Fatalf("WritePoint WMaxLimPct: %v", err)
		}
		if p.ScaledValue == nil || *p.ScaledValue != 62.5 {
			t.Errorf("WMaxLimPct reads back %+v", p)
		}
		if _, err := dev.WritePoint(ctx, *controls, "OutPFSet_Ena", "ENABLED"); err != nil {
			t.Fatalf("WritePoint OutPFSet_Ena: %v", err)
		}
		if _, err := dev.WritePoint(ctx, *dev.ModelByID(103), "W", 1); !errors.Is(err, sunspec.ErrPointNotWritable) {
			t.Errorf("W of model 103: %v, want ErrPointNotWritable", err)
		}

		var scan peerScan
		if code := peerClient(t, a, &scan, "scan", ip, containerPort, fx.UnitID); code != 0 {
			t.Fatalf("%s scan: exit %d", a.name, code)
		}
		for _, m := range scan.Models {
			if m.ID != 123 {
				continue
			}
			if !sameRaw(uint16(625), m.Values["WMaxLimPct"]) || !sameRaw(uint16(1), m.Values["OutPFSet_Ena"]) {
				t.Errorf("%s reads WMaxLimPct %v, OutPFSet_Ena %v", a.name, m.Values["WMaxLimPct"], m.Values["OutPFSet_Ena"])
			}
			if !sameRaw(uint16(1), m.Values["Conn"]) {
				t.Errorf("%s: Conn changed to %v", a.name, m.Values["Conn"])
			}
		}
	})
}

// TestGoWritesPeerDER writes IEEE 1547 settings of the peer's DER device with
// WritePoint, a scaled capacity setting and a curve adoption request, and the
// peer's own client reads them back.
func TestGoWritesPeerDER(t *testing.T) {
	forEachPeer(t, func(t *testing.T, a peer) {
		fx := loadFixture(t, a, "der")
		ip, port := startDevice(t, a, "der")
		client, err := modbus.New(modbus.Config{URL: fmt.Sprintf("tcp://127.0.0.1:%d", port), Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Open(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		dev, err := sunspec.Open(ctx, client, &sunspec.DiscoverOptions{UnitID: fx.UnitID})
		if err != nil {
			t.Fatal(err)
		}
		settings, curves := dev.ModelByID(702), dev.ModelByID(705)
		if settings == nil || curves == nil {
			t.Fatal("no model 702 or 705")
		}
		// W_SF of model 702 is 1 in the fixture: 9000 W is 900.
		p, err := dev.WritePoint(ctx, *settings, "WMax", 9000)
		if err != nil {
			t.Fatalf("WritePoint WMax: %v", err)
		}
		if p.ScaledValue == nil || *p.ScaledValue != 9000 {
			t.Errorf("WMax reads back %+v", p)
		}
		if _, err := dev.WritePoint(ctx, *curves, "AdptCrvReq", 2); err != nil {
			t.Fatalf("WritePoint AdptCrvReq: %v", err)
		}
		if _, err := dev.WritePoint(ctx, *curves, "AdptCrvRslt", 2); !errors.Is(err, sunspec.ErrPointNotWritable) {
			t.Errorf("AdptCrvRslt of model 705: %v, want ErrPointNotWritable", err)
		}

		var scan peerScan
		if code := peerClient(t, a, &scan, "scan", ip, containerPort, fx.UnitID); code != 0 {
			t.Fatalf("%s scan: exit %d", a.name, code)
		}
		for _, m := range scan.Models {
			switch m.ID {
			case 702:
				if !sameRaw(uint16(900), m.Values["WMax"]) {
					t.Errorf("%s reads WMax %v", a.name, m.Values["WMax"])
				}
			case 705:
				if !sameRaw(uint16(2), m.Values["AdptCrvReq"]) {
					t.Errorf("%s reads AdptCrvReq %v", a.name, m.Values["AdptCrvReq"])
				}
			}
		}
	})
}

// TestWorkflowPin keeps the Interop workflow on the images of this file.
func TestWorkflowPin(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "interop.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range references {
		if !strings.Contains(string(data), r.env+": "+r.image+"\n") {
			t.Errorf("the workflow does not pin %s as %s", r.env, r.image)
		}
		if !regexp.MustCompile(`:v\d+\.\d+\.\d+@sha256:[0-9a-f]{64}$`).MatchString(r.image) {
			t.Errorf("%s is not pinned by digest", r.image)
		}
	}
}
