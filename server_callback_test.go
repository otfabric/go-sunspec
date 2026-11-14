// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
)

// The callback contract of ServerOptions.OnWrite and Server.Update, rows L9
// (a callback calls a public method), L10 (a callback ends Serve), L11 (a
// slow callback) and L15 (Serve's shutdown cut short) of the lifecycle matrix
// in TESTING.md. Every wait is on an event; callbackTimeout only bounds a
// wait that would otherwise not end, and its failure says what did not
// happen.

const callbackTimeout = 10 * time.Second

// recv waits for a value or fails the test naming what did not arrive.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	var v T
	select {
	case v = <-ch:
	case <-time.After(callbackTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
	return v
}

// gate returns a channel a callback blocks on and the function that opens
// it. The gate also opens when the test ends, before the cleanup of what was
// started before the gate was made: a test that fails with its callback
// still blocked does not leave a server waiting for that callback when it
// stops.
func gate(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	ch := make(chan struct{})
	open := sync.OnceFunc(func() { close(ch) })
	t.Cleanup(open)
	return ch, open
}

// goErr runs fn on a goroutine and returns a channel with its error.
func goErr(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	return ch
}

// twoCtlServer serves two ctl models from base address 40000 and returns
// the address of the Rw point of each.
func twoCtlServer(t *testing.T, opts *ServerOptions) (srv *Server, rw [2]uint16) {
	t.Helper()
	if opts == nil {
		opts = &ServerOptions{}
	}
	opts.BaseAddress = 40000
	srv, err := NewServer(opts,
		ServerModel{Schema: ctlSchema(), Values: ctlValues()},
		ServerModel{Schema: ctlSchema(), Values: ctlValues()})
	if err != nil {
		t.Fatal(err)
	}
	for i, h := range srv.Models() {
		rw[i] = h.StartAddress + 3 // ID, L, Ro, then Rw
	}
	return srv, rw
}

// TestL09OnWriteCallsServerMethods: OnWrite may call every method of the
// Server. The ones that read see the model as it was before the write.
func TestL09OnWriteCallsServerMethods(t *testing.T) {
	type seen struct {
		headers []ModelHeader
		before  any
		other   any
		err     error
	}
	got := make(chan seen, 1)
	var srv *Server
	srv, rw := twoCtlServer(t, &ServerOptions{OnWrite: func(_ context.Context, w *ServerWrite) error {
		var s seen
		s.headers = srv.Models()
		var dm *DecodedModel
		if dm, s.err = srv.Model(w.ModelIndex); s.err == nil {
			s.before = dm.Point("Rw").RawValue
			if dm, s.err = srv.Model(1); s.err == nil {
				s.other = dm.Point("Rw").RawValue
			}
		}
		got <- s
		return nil
	}})

	done := goErr(func() error { return holdingWrite(srv, rw[0], 33) })
	s := recv(t, got, "OnWrite")
	if err := recv(t, done, "the write to return"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if s.err != nil || len(s.headers) != 2 || s.before != uint16(22) || s.other != uint16(22) {
		t.Fatalf("OnWrite saw %+v, want two models and Rw 22 in both", s)
	}
	if v := servedPoint(t, srv, 0, "Rw"); v != uint16(33) {
		t.Fatalf("after the write Rw is %v, want 33", v)
	}
}

// TestL09OnWriteChangesItsModel: a change of the written model made while
// OnWrite runs, here by OnWrite itself, refuses the write with exception 4
// and keeps the change.
func TestL09OnWriteChangesItsModel(t *testing.T) {
	var srv *Server
	srv, rw := twoCtlServer(t, &ServerOptions{OnWrite: func(_ context.Context, w *ServerWrite) error {
		if w.ModelIndex == 0 {
			return srv.SetPoint(0, "Ro", 44)
		}
		// A change of another model does not refuse the write.
		return srv.SetPoint(0, "Ro", 55)
	}})

	err := recv(t, goErr(func() error { return holdingWrite(srv, rw[0], 33) }), "the write to return")
	if !errors.Is(err, modbus.ErrServerDeviceFailure) {
		t.Fatalf("write while OnWrite changed the model: %v, want modbus.ErrServerDeviceFailure", err)
	}
	if ro, rwv := servedPoint(t, srv, 0, "Ro"), servedPoint(t, srv, 0, "Rw"); ro != uint16(44) || rwv != uint16(22) {
		t.Fatalf("after the refused write: Ro %v, Rw %v; want 44 (OnWrite's change) and 22 (unchanged)", ro, rwv)
	}

	if err := recv(t, goErr(func() error { return holdingWrite(srv, rw[1], 66) }), "the write to return"); err != nil {
		t.Fatalf("write of model 1 while OnWrite changed model 0: %v", err)
	}
	if ro, rwv := servedPoint(t, srv, 0, "Ro"), servedPoint(t, srv, 1, "Rw"); ro != uint16(55) || rwv != uint16(66) {
		t.Fatalf("after the write of model 1: model 0 Ro %v, model 1 Rw %v; want 55 and 66", ro, rwv)
	}
}

// TestL09UpdateCallsServerMethods: the function given to Update may read
// the Server.
func TestL09UpdateCallsServerMethods(t *testing.T) {
	srv, _ := twoCtlServer(t, nil)
	var before any
	err := recv(t, goErr(func() error {
		return srv.Update(0, func(v *Values) error {
			dm, err := srv.Model(0)
			if err != nil {
				return err
			}
			before = dm.Point("Rw").RawValue
			v.Set("Rw", len(srv.Models()))
			return nil
		})
	}), "Update to return")
	if err != nil || before != uint16(22) {
		t.Fatalf("Update: %v; fn read Rw %v, want 22", err, before)
	}
	if v := servedPoint(t, srv, 0, "Rw"); v != uint16(2) {
		t.Fatalf("after Update Rw is %v, want 2", v)
	}
}

// TestUpdateRunsAgainAfterAChange: when the model changes while Update's
// function runs, the function is called again with the new values, and
// only its last result is stored.
func TestUpdateRunsAgainAfterAChange(t *testing.T) {
	srv, rw := twoCtlServer(t, nil)
	entered := make(chan struct{})
	proceed, letGo := gate(t)
	var seen []any
	done := goErr(func() error {
		return srv.Update(0, func(v *Values) error {
			seen = append(seen, v.Points["Rw"])
			if len(seen) == 1 {
				entered <- struct{}{}
				<-proceed
			}
			v.Set("Ro", 99)
			return nil
		})
	})
	recv(t, entered, "the first call of fn")
	// A client write and an update of the same model while fn runs.
	if err := recv(t, goErr(func() error { return holdingWrite(srv, rw[0], 33) }), "the client write"); err != nil {
		t.Fatalf("client write while fn runs: %v", err)
	}
	letGo()
	if err := recv(t, done, "Update to return"); err != nil {
		t.Fatal(err)
	}
	// The first value is the one NewServer was given (an int), the second
	// the one decoded from the client's write.
	if fmt.Sprint(seen) != "[22 33]" {
		t.Fatalf("fn saw Rw %v, want 22 and then 33", seen)
	}
	if ro, rwv := servedPoint(t, srv, 0, "Ro"), servedPoint(t, srv, 0, "Rw"); ro != uint16(99) || rwv != uint16(33) {
		t.Fatalf("after Update: Ro %v, Rw %v; want 99 and 33", ro, rwv)
	}
}

// TestL11SlowOnWrite: while OnWrite blocks for a write of one model, other
// clients read every model, the one being written included, and write
// another model.
func TestL11SlowOnWrite(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	srv, rw := twoCtlServer(t, &ServerOptions{OnWrite: func(_ context.Context, w *ServerWrite) error {
		if w.ModelIndex == 0 {
			entered <- struct{}{}
			<-release
		}
		return nil
	}})
	addr, _ := startModbus(t, srv)
	writer, reader := dial(t, addr, callbackTimeout), dial(t, addr, callbackTimeout)
	// Registered after the server and the clients, so that it runs before
	// they are stopped.
	letGo := sync.OnceFunc(func() { close(release) })
	t.Cleanup(letGo)
	ctx := context.Background()

	slow := goErr(func() error { return writer.WriteRegisters(ctx, 1, rw[0], []uint16{33}) })
	recv(t, entered, "OnWrite of model 0")

	type read struct {
		regs []uint16
		err  error
	}
	reads := make(chan read, 1)
	go func() {
		regs, err := reader.ReadRegisters(ctx, 1, rw[0], rw[1]-rw[0]+1, modbus.HoldingRegister)
		reads <- read{regs, err}
	}()
	r := recv(t, reads, "a read of both models while OnWrite blocks")
	if r.err != nil || r.regs[0] != 22 || r.regs[len(r.regs)-1] != 22 {
		t.Fatalf("read while OnWrite blocks: %v, %v; want Rw 22 in both models", r.regs, r.err)
	}
	if err := recv(t, goErr(func() error { return reader.WriteRegisters(ctx, 1, rw[1], []uint16{44}) }),
		"a write of model 1 while OnWrite of model 0 blocks"); err != nil {
		t.Fatalf("write of model 1: %v", err)
	}

	letGo()
	if err := recv(t, slow, "the slow write"); err != nil {
		t.Fatalf("slow write: %v", err)
	}
	if a, b := servedPoint(t, srv, 0, "Rw"), servedPoint(t, srv, 1, "Rw"); a != uint16(33) || b != uint16(44) {
		t.Fatalf("after the writes: Rw %v and %v, want 33 and 44", a, b)
	}
}

// TestL11SlowUpdate: while the function given to Update blocks, clients
// read the model.
func TestL11SlowUpdate(t *testing.T) {
	srv, rw := twoCtlServer(t, nil)
	addr, _ := startModbus(t, srv)
	client := dial(t, addr, callbackTimeout)
	entered := make(chan struct{})
	release, letGo := gate(t)
	done := goErr(func() error {
		return srv.Update(0, func(v *Values) error {
			entered <- struct{}{}
			<-release
			v.Set("Rw", 7)
			return nil
		})
	})
	recv(t, entered, "Update's function")
	regs, err := recvRead(t, client, rw[0])
	if err != nil || regs[0] != 22 {
		t.Fatalf("read while Update's function blocks: %v, %v", regs, err)
	}
	letGo()
	if err := recv(t, done, "Update to return"); err != nil {
		t.Fatal(err)
	}
	if regs, err := recvRead(t, client, rw[0]); err != nil || regs[0] != 7 {
		t.Fatalf("read after Update: %v, %v", regs, err)
	}
}

// recvRead reads one register through client, bounded.
func recvRead(t *testing.T, client *modbus.Client, addr uint16) ([]uint16, error) {
	t.Helper()
	type read struct {
		regs []uint16
		err  error
	}
	ch := make(chan read, 1)
	go func() {
		regs, err := client.ReadRegisters(context.Background(), 1, addr, 1, modbus.HoldingRegister)
		ch <- read{regs, err}
	}()
	r := recv(t, ch, "a read")
	return r.regs, r.err
}

// TestOnWriteThatDoesNotReturnNormally: behind a go-modbus server, an OnWrite
// that does not return normally answers the client with exception 4; nothing
// changes and the Server keeps serving.
func TestOnWriteThatDoesNotReturnNormally(t *testing.T) {
	srv, rw := twoCtlServer(t, &ServerOptions{OnWrite: func(context.Context, *ServerWrite) error {
		panic("callback failure")
	}})
	addr, _ := startModbus(t, srv)
	client := dial(t, addr, callbackTimeout)
	err := recv(t, goErr(func() error { return client.WriteRegisters(context.Background(), 1, rw[0], []uint16{33}) }), "the write")
	if !errors.Is(err, modbus.ErrServerDeviceFailure) {
		t.Fatalf("write with an OnWrite that does not return normally: %v, want modbus.ErrServerDeviceFailure", err)
	}
	if regs, err := recvRead(t, client, rw[0]); err != nil || regs[0] != 22 {
		t.Fatalf("read after that write: %v, %v; want Rw 22", regs, err)
	}
	if err := srv.SetPoint(0, "Rw", 5); err != nil {
		t.Fatalf("SetPoint after that write: %v", err)
	}
}

// TestOnWriteOwnsServerWrite: what OnWrite changes in the ServerWrite it is
// given is not stored: the device holds the registers the client wrote.
func TestOnWriteOwnsServerWrite(t *testing.T) {
	srv, rw := twoCtlServer(t, &ServerOptions{OnWrite: func(_ context.Context, w *ServerWrite) error {
		w.Model.RawRegisters[2] = 999 // Ro
		w.Model.RawRegisters[3] = 888 // Rw
		w.Model.Group.Points[2].RawValue = uint16(777)
		w.Points[0].RawValue = uint16(666)
		w.Registers[0] = 555
		return nil
	}})
	before, _ := holdingRead(srv, 40000, uint16(len(srv.image)))
	if err := holdingWrite(srv, rw[0], 33); err != nil {
		t.Fatal(err)
	}
	after, _ := holdingRead(srv, 40000, uint16(len(srv.image)))
	want := slices.Clone(before)
	want[rw[0]-40000] = 33
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("image after the write:\n got %v\nwant %v", after, want)
	}
	if ro, rwv := servedPoint(t, srv, 0, "Ro"), servedPoint(t, srv, 0, "Rw"); ro != uint16(11) || rwv != uint16(33) {
		t.Fatalf("values after the write: Ro %v, Rw %v; want 11 and 33", ro, rwv)
	}
	// The stored values are the written registers too: a later update
	// re-encodes them.
	if err := srv.SetPoint(0, "Name", "zz"); err != nil {
		t.Fatal(err)
	}
	if ro := servedPoint(t, srv, 0, "Ro"); ro != uint16(11) {
		t.Fatalf("after SetPoint Ro is %v, want 11", ro)
	}
}

// TestUpdateValuesKeptAsDocumented: Update's function keeps a copy of v, as
// the documentation says, and changes it after returning; the served values
// do not change (run with -race).
func TestUpdateValuesKeptAsDocumented(t *testing.T) {
	srv, _ := twoCtlServer(t, nil)
	var kept *Values
	if err := srv.Update(0, func(v *Values) error {
		v.Set("Rw", 1)
		kept = v.Clone()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		kept.Set("Rw", 2).Set("Ro", 3)
	}()
	if err := srv.SetPoint(0, "Name", "zz"); err != nil {
		t.Fatal(err)
	}
	<-done
	if ro, rwv := servedPoint(t, srv, 0, "Ro"), servedPoint(t, srv, 0, "Rw"); ro != uint16(11) || rwv != uint16(1) {
		t.Fatalf("served Ro %v, Rw %v; want 11 and 1", ro, rwv)
	}
}

// TestL10OnWriteEndsServe: no type here has a Close; what comes nearest is a
// callback that ends Serve by cancelling its context. Serve waits for the
// callback's request, which was accepted, and returns nil.
func TestL10OnWriteEndsServe(t *testing.T) {
	leaks := leakCheck(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, rw := twoCtlServer(t, &ServerOptions{OnWrite: func(context.Context, *ServerWrite) error {
		cancel()
		return nil
	}})
	addr := listenAddr(t)
	served := goErr(func() error {
		return srv.Serve(ctx, &modbus.ServerConfig{URL: "tcp://" + addr, Logger: modbus.NopLogger()})
	})
	client := dial(t, addr, callbackTimeout)
	// The answer may or may not reach the client before its connection is
	// shut down.
	write := goErr(func() error { return client.WriteRegisters(context.Background(), 1, rw[0], []uint16{33}) })

	if err := recv(t, served, "Serve to return"); err != nil {
		t.Fatalf("Serve ended by its OnWrite: %v, want nil", err)
	}
	if err := recv(t, write, "the write to return"); err != nil {
		t.Logf("the answer did not reach the client: %v", err)
	}
	if v := servedPoint(t, srv, 0, "Rw"); v != uint16(33) {
		t.Fatalf("after the write Rw is %v, want 33", v)
	}
	_ = client.Close()
	leaks()
}

// TestL15ServeShutdownCutShort: when a connection does not end within the
// shutdown timeout, here because OnWrite does not return, Serve returns an
// error matching context.DeadlineExceeded and does not wait for it.
func TestL15ServeShutdownCutShort(t *testing.T) {
	saved := serveShutdownTimeout
	serveShutdownTimeout = 50 * time.Millisecond
	t.Cleanup(func() { serveShutdownTimeout = saved })
	leaks := leakCheck(t)

	entered := make(chan struct{}, 1)
	release, letGo := gate(t)
	srv, rw := twoCtlServer(t, &ServerOptions{OnWrite: func(context.Context, *ServerWrite) error {
		entered <- struct{}{}
		<-release // ignores ctx: a callback that does not end
		return nil
	}})
	addr := listenAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	served := goErr(func() error {
		return srv.Serve(ctx, &modbus.ServerConfig{URL: "tcp://" + addr, Logger: modbus.NopLogger()})
	})
	client := dial(t, addr, callbackTimeout)
	write := goErr(func() error { return client.WriteRegisters(context.Background(), 1, rw[0], []uint16{33}) })
	recv(t, entered, "OnWrite")

	cancel()
	if err := recv(t, served, "Serve to return"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Serve with a connection that does not end: %v, want context.DeadlineExceeded", err)
	}
	// The connection was closed under the blocked request: the client gets
	// an error once OnWrite returns.
	letGo()
	if err := recv(t, write, "the write to fail"); err == nil {
		t.Fatal("the write succeeded on a connection that was shut down")
	}
	_ = client.Close()
	leaks()
}
