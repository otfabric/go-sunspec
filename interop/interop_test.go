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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"
)

// release is the otfabric/sunspec-interop release of defaultImage, whose
// digest comes from the manifest of that release. Keep it in step with the
// Interop workflow (TestWorkflowPin).
const (
	release      = "v0.1.0"
	defaultImage = "ghcr.io/otfabric/sunspec-interop-pysunspec2:" + release +
		"@sha256:4e7cae22cff30b282966cbf1c777016286b623afecba85030011040cb86c4a17"
)

const (
	imageEnv      = "SUNSPEC_INTEROP_PYSUNSPEC2_IMAGE"
	requireEnv    = "SUNSPEC_REQUIRE_INTEROP"
	containerPort = 1502
	hostGateway   = "host.docker.internal"
	addHost       = hostGateway + ":host-gateway"
	relTolerance  = 1e-6
)

var fixtures = []string{"baseline", "edge", "altbase"}

func image() string {
	if v := os.Getenv(imageEnv); v != "" {
		return v
	}
	return defaultImage
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

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	out, stderr, err := docker("run", "--rm", image(), "print-fixture", name)
	if err != nil {
		t.Fatalf("print-fixture %s: %v: %s", name, err, stderr)
	}
	var fx fixture
	decode(t, out, &fx)
	return fx
}

// peerModel is one model as pysunspec2 read it.
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
func peerClient(t *testing.T, into any, op, host string, port int, unit uint8, flags ...string) int {
	t.Helper()
	args := append([]string{"run", "--rm", "--add-host", addHost, image(), "client", op,
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
func startDevice(t *testing.T, fixture string) (string, int) {
	t.Helper()
	out, stderr, err := docker("run", "-d", "--rm", "-p", "127.0.0.1::1502", image(), "server", "--fixture", "/fixtures/"+fixture+"/fixture.json")
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
// instances, with what pysunspec2 read.
func compareGroup(t *testing.T, where string, g *sunspec.DecodedGroup, raw, scaled map[string]any) {
	t.Helper()
	for i := range g.Points {
		pt := &g.Points[i]
		path := where + "." + pt.Name
		peer, ok := raw[pt.Name]
		if !ok {
			t.Errorf("%s: pysunspec2 has no such point", path)
			continue
		}
		if peer == nil {
			if pt.Implemented {
				t.Errorf("%s: read %v, pysunspec2 reads not implemented", path, pt.RawValue)
			}
			continue
		}
		if !pt.Implemented {
			t.Errorf("%s: not implemented, pysunspec2 reads %v", path, peer)
			continue
		}
		if !sameRaw(pt.RawValue, peer) {
			t.Errorf("%s: read %#v, pysunspec2 reads %v", path, pt.RawValue, peer)
		}
		if pt.ScaledValue != nil {
			n, ok := scaled[pt.Name].(json.Number)
			if !ok {
				t.Errorf("%s: scaled %v, pysunspec2 reads %v", path, *pt.ScaledValue, scaled[pt.Name])
				continue
			}
			if f, err := n.Float64(); err != nil || !near(*pt.ScaledValue, f) {
				t.Errorf("%s: scaled %v, pysunspec2 reads %v", path, *pt.ScaledValue, n)
			}
		}
	}
	seen := map[string]int{}
	for _, inst := range g.Groups {
		rawList, _ := raw[inst.Name].([]any)
		scaledList, _ := scaled[inst.Name].([]any)
		i := seen[inst.Name]
		seen[inst.Name]++
		if i >= len(rawList) || i >= len(scaledList) {
			t.Errorf("%s.%s[%d]: pysunspec2 has %d instances", where, inst.Name, i, len(rawList))
			continue
		}
		compareGroup(t, fmt.Sprintf("%s.%s[%d]", where, inst.Name, i), inst, rawList[i].(map[string]any), scaledList[i].(map[string]any))
	}
}

// compareDevice compares a whole device: the model chain, then every point.
func compareDevice(t *testing.T, models []*sunspec.DecodedModel, scan *peerScan) {
	t.Helper()
	if len(models) != len(scan.Models) {
		t.Fatalf("%d models, pysunspec2 reads %d", len(models), len(scan.Models))
	}
	points := 0
	for i, m := range models {
		p := &scan.Models[i]
		where := fmt.Sprintf("model %d", m.ModelID)
		if p.Error != "" {
			t.Errorf("%s: pysunspec2: %s", where, p.Error)
		}
		if m.ModelID != p.ID || m.InstanceAddress != p.Address {
			t.Errorf("%s at %d: pysunspec2 reads model %d at %d", where, m.InstanceAddress, p.ID, p.Address)
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

// valuesFrom builds encoder input from what pysunspec2 read: the encoder takes
// scaled values, and a point pysunspec2 reads as not implemented is left out.
func valuesFrom(scaled map[string]any, top bool) *sunspec.Values {
	v := &sunspec.Values{}
	for name, value := range scaled {
		if top && (name == "ID" || name == "L") {
			continue
		}
		switch x := value.(type) {
		case nil:
		case []any:
			for _, inst := range x {
				v.Add(name, valuesFrom(inst.(map[string]any), false))
			}
		default:
			v.Set(name, x)
		}
	}
	return v
}

// peerRegisters is the map pysunspec2 encodes for a fixture.
func peerRegisters(t *testing.T, name string) []uint16 {
	t.Helper()
	out, stderr, err := docker("run", "--rm", image(), "print-registers", name)
	if err != nil {
		t.Fatalf("print-registers %s: %v: %s", name, err, stderr)
	}
	var doc struct {
		Registers []uint16 `json:"registers"`
	}
	decode(t, out, &doc)
	return doc.Registers
}

// servedFrom lays out a Server with what pysunspec2 reads from a fixture.
func servedFrom(t *testing.T, fx fixture, scan *peerScan) *sunspec.Server {
	t.Helper()
	models := make([]sunspec.ServerModel, len(scan.Models))
	for i := range scan.Models {
		models[i] = sunspec.ServerModel{ID: scan.Models[i].ID, Values: valuesFrom(scan.Models[i].Scaled, true)}
	}
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{UnitID: fx.UnitID, BaseAddress: fx.BaseAddress}, models...)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// TestGoReadsPysunspec2Device reads a device pysunspec2 encoded through the
// public client path, and compares every point with what pysunspec2's own
// client reads from the same device.
func TestGoReadsPysunspec2Device(t *testing.T) {
	requireDocker(t)
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			fx := loadFixture(t, name)
			ip, port := startDevice(t, name)
			var scan peerScan
			if code := peerClient(t, &scan, "scan", ip, containerPort, fx.UnitID); code != 0 {
				t.Fatalf("pysunspec2 scan: exit %d", code)
			}
			if scan.BaseAddress != fx.BaseAddress {
				t.Fatalf("pysunspec2 finds base address %d, fixture %d", scan.BaseAddress, fx.BaseAddress)
			}
			compareDevice(t, openDevice(t, port, fx.UnitID), &scan)
		})
	}
}

// TestPysunspec2ReadsGoServer serves through Server the values pysunspec2
// read, and compares the registers with what pysunspec2 encodes, then what
// pysunspec2's client reads from the Server with what go-sunspec reads.
func TestPysunspec2ReadsGoServer(t *testing.T) {
	requireDocker(t)
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			fx := loadFixture(t, name)
			ip, _ := startDevice(t, name)
			var want peerScan
			if code := peerClient(t, &want, "scan", ip, containerPort, fx.UnitID); code != 0 {
				t.Fatalf("pysunspec2 scan of its own device: exit %d", code)
			}
			srv := servedFrom(t, fx, &want)
			port := startServer(t, srv)
			var got peerScan
			if code := peerClient(t, &got, "scan", hostGateway, port, fx.UnitID); code != 0 {
				t.Fatalf("pysunspec2 scan of Server: exit %d", code)
			}
			if got.BaseAddress != fx.BaseAddress {
				t.Fatalf("pysunspec2 finds base address %d, fixture %d", got.BaseAddress, fx.BaseAddress)
			}
			compareDevice(t, openDevice(t, port, fx.UnitID), &got)
			compareRegisters(t, srv, peerRegisters(t, name))
		})
	}
}

// compareRegisters compares the Server's image, model by model, with the map
// pysunspec2 encodes for the same values.
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
			t.Fatalf("model %d: past the end of pysunspec2's %d registers", m.ModelID, len(want))
		}
		for j, r := range regs {
			if want[i+j] != r {
				t.Errorf("model %d offset %d: %#04x, pysunspec2 encodes %#04x", m.ModelID, j, r, want[i+j])
			}
		}
		i += len(regs)
	}
	if i != len(want)-2 {
		t.Errorf("%d registers before the end model, pysunspec2 has %d", i, len(want)-2)
	}
}

// TestPysunspec2WritesGoServer writes a read-write point with pysunspec2's
// client, and is refused on a read-only point.
func TestPysunspec2WritesGoServer(t *testing.T) {
	requireDocker(t)
	const (
		model = uint16(123)
		point = "WMaxLimPct"
		value = 750
	)
	fx := loadFixture(t, "baseline")
	ip, _ := startDevice(t, "baseline")
	var want peerScan
	if code := peerClient(t, &want, "scan", ip, containerPort, fx.UnitID); code != 0 {
		t.Fatalf("pysunspec2 scan: exit %d", code)
	}
	srv := servedFrom(t, fx, &want)
	port := startServer(t, srv)

	var res struct {
		Value json.Number `json:"value"`
	}
	if code := peerClient(t, &res, "write", hostGateway, port, fx.UnitID, "--model", strconv.Itoa(int(model)), "--point", point, "--value", strconv.Itoa(value)); code != 0 {
		t.Fatalf("write: exit %d", code)
	}
	if res.Value.String() != strconv.Itoa(value) {
		t.Errorf("pysunspec2 reads back %s", res.Value)
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

	if code := peerClient(t, nil, "write", hostGateway, port, fx.UnitID, "--model", "103", "--point", "W", "--value", "1"); code != 1 {
		t.Errorf("write of a read-only point: exit %d, want 1", code)
	}
}

// TestWorkflowPin keeps the Interop workflow on the image of this file.
func TestWorkflowPin(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "interop.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), imageEnv+": "+defaultImage+"\n") {
		t.Errorf("the workflow does not pin %s as %s", imageEnv, defaultImage)
	}
	if !regexp.MustCompile(`:v\d+\.\d+\.\d+@sha256:[0-9a-f]{64}$`).MatchString(defaultImage) {
		t.Errorf("%s is not pinned by digest", defaultImage)
	}
}
