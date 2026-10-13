// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
	"github.com/otfabric/go-sunspec/registry"
)

// ServerModel defines one model of a served device.
//
// A model is given either by values, which are encoded with a schema, or by
// its data registers. The first form supports updates by point name and
// client writes; the second serves any model, including vendor models the
// registry does not know, as an opaque read-only block.
type ServerModel struct {
	// ID is the SunSpec model ID. It may be left zero when Schema is set.
	ID uint16
	// Schema is the schema the values are encoded with. Nil selects the
	// registry's schema for ID. It is ignored when Registers is set.
	Schema *registry.ModelMeta
	// Values holds the point values, see Values. Nil serves the model with
	// every point not implemented. It must be nil when Registers is set.
	Values *Values
	// Registers holds the model's data registers: what follows the ID and
	// length registers. When it is not nil the model is served from these
	// registers as they are, its length is len(Registers), and no schema is
	// applied. It must not be empty.
	Registers []uint16
}

// ServerOptions configures a Server. A nil *ServerOptions selects unit ID 1,
// base address 0 and holding registers.
type ServerOptions struct {
	// UnitID is the only Modbus unit ID the device answers. Zero selects
	// unit 1, as in DiscoverOptions.
	UnitID uint8
	// BaseAddress is the register address of the "SunS" marker. The zero
	// value is address 0, one of the standard SunSpec base addresses; most
	// devices use 40000. A client only finds the device at an address it
	// probes (SunSpecDefaultBaseAddresses for this package's Discover).
	BaseAddress uint16
	// RegType is the register type the device map is served in. The zero
	// value is modbus.HoldingRegister, which is what nearly all SunSpec
	// devices use. With modbus.InputRegister the map is read-only: Modbus
	// has no function to write input registers.
	RegType modbus.RegType
	// OnWrite, when not nil, is called for every client write that passed
	// the Server's own checks, before it takes effect. Returning nil accepts
	// the write. Returning an error rejects it: nothing changes and the
	// client receives that error as a Modbus exception (return
	// modbus.ErrIllegalDataValue to refuse a value; an error that is not a
	// Modbus exception is reported as a server device failure).
	//
	// OnWrite runs while the Server is locked, so that the write is atomic.
	// It must not call methods of the Server and should return quickly.
	OnWrite func(ctx context.Context, w *ServerWrite) error
}

// ServerWrite describes a client write to a served device, as passed to
// ServerOptions.OnWrite.
type ServerWrite struct {
	// ClientAddr is the address of the client, as reported by go-modbus.
	ClientAddr string
	// ModelIndex is the position of the written model in the device's chain,
	// starting at 0.
	ModelIndex int
	// ModelID is the SunSpec ID of the written model.
	ModelID uint16
	// Address is the address of the first register written.
	Address uint16
	// Registers holds the register values written.
	Registers []uint16
	// Points holds the points covered by the write, in register order,
	// decoded as they will be once the write is accepted. RegisterOffset
	// tells instances of a repeating group apart.
	Points []DecodedPoint
	// Model is the whole model as it will be once the write is accepted.
	Model *DecodedModel
}

// Server is a SunSpec device held in memory and served over Modbus: the
// "SunS" marker at the base address, then each model (ID and length
// registers, data), then the end marker.
//
// A Server is a go-modbus RequestHandler. It does not listen itself: pass it
// to modbus.NewServer, which owns the listener, the connections, logging,
// metrics and TLS, or call Serve for the common case.
//
//	srv, err := sunspec.NewServer(&sunspec.ServerOptions{BaseAddress: 40000}, models...)
//	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://0.0.0.0:1502"}, srv)
//	err = ms.Start()
//
// # Requests
//
// Reads of the configured register type are answered for any range inside the
// device map, also one that spans several models. A range that touches an
// address outside the map is answered with exception 2 (illegal data
// address), as is any request for the other register type. A request for
// another unit ID is answered with exception 0x0B (gateway target device
// failed to respond). Coils and discrete inputs do not exist: exception 1
// (illegal function).
//
// # Writes
//
// A client may write (function codes 6 and 16) the points whose schema access
// is "RW", in a model served from values. A write must cover whole points and
// nothing else. Writing anything else is refused with exception 2 and changes
// nothing: read-only points, the marker, the ID and length registers, the
// count point of a group, part of a multi-register point, a model served from
// raw registers, an address outside the map. The Server stores the registers
// of an accepted write as they are; it does not judge values. Give an OnWrite
// function to validate, refuse or observe writes. Function codes 22 (mask
// write) and 23 (read/write) are not supported.
//
// # Updates and concurrency
//
// All methods are safe for concurrent use with each other and with requests
// being served. Each update (SetPoint, SetValues, Update, SetRegisters) and
// each accepted client write replaces a model's registers atomically: a
// single Modbus read never sees half of it. A client that needs several
// requests for one model (more than 125 registers) can see different updates
// in different requests, as with any Modbus device.
//
// The layout is fixed when the Server is created. An update cannot change the
// length of a model.
type Server struct {
	unitID  uint8
	base    uint16
	regType modbus.RegType
	onWrite func(ctx context.Context, w *ServerWrite) error

	mu sync.RWMutex
	// image holds the device map from the marker to the end marker;
	// image[0] is the register at base.
	image  []uint16
	models []*servedModel
}

// servedModel is one model in a Server's map.
type servedModel struct {
	id uint16
	// schema is nil for a model served from raw registers.
	schema *registry.ModelMeta
	// values are the values the registers were last encoded from, or decoded
	// from after a client write.
	values *Values
	// start is the index in image of the model's ID register; the model
	// occupies length registers from there, header included.
	start, length int
	slots         []pointSlot
}

// NewServer lays out a device from the given models, in order.
//
// It returns the error of EncodeModel for a model whose values cannot be
// encoded, an error matching ErrUnknownModel for a model given without
// registers whose ID has no schema in the registry, and an error matching
// ErrModelLayout when a model has no data registers, uses the end marker's ID
// 65535, is longer than 65535 registers, or the chain does not fit between
// the base address and address 65535. A device without models is valid: it
// serves the marker and the end marker.
//
// The models' values and registers are copied.
func NewServer(opts *ServerOptions, models ...ServerModel) (*Server, error) {
	s := &Server{unitID: 1}
	if opts != nil {
		if opts.UnitID != 0 {
			s.unitID = opts.UnitID
		}
		s.base = opts.BaseAddress
		s.regType = opts.RegType
		s.onWrite = opts.OnWrite
	}
	if s.regType != modbus.HoldingRegister && s.regType != modbus.InputRegister {
		return nil, fmt.Errorf("sunspec: server: invalid register type %d: %w", s.regType, modbus.ErrUnexpectedParameters)
	}

	s.image = []uint16{gmsunspec.MarkerReg0, gmsunspec.MarkerReg1}
	for i := range models {
		sm, regs, err := buildServedModel(&models[i])
		if err != nil {
			return nil, fmt.Errorf("sunspec: server: model %d of the chain: %w", i, err)
		}
		if sm.id == gmsunspec.EndModelID {
			return nil, fmt.Errorf("sunspec: server: model %d of the chain: ID %d is the end marker: %w", i, sm.id, ErrModelLayout)
		}
		if len(regs) <= 2 || len(regs) > maxModelRegisters {
			return nil, fmt.Errorf("sunspec: server: model %d of the chain (ID %d): %d data registers, need 1 to 65535: %w",
				i, sm.id, len(regs)-2, ErrModelLayout)
		}
		sm.start, sm.length = len(s.image), len(regs)
		s.image = append(s.image, regs...)
		s.models = append(s.models, sm)
		if int(s.base)+len(s.image)+2 > 0x10000 {
			return nil, fmt.Errorf("sunspec: server: model %d of the chain (ID %d) ends past address 65535 from base address %d: %w",
				i, sm.id, s.base, ErrModelLayout)
		}
	}
	s.image = append(s.image, gmsunspec.EndModelID, gmsunspec.EndModelLength)
	if int(s.base)+len(s.image) > 0x10000 {
		return nil, fmt.Errorf("sunspec: server: the device map does not fit at base address %d: %w", s.base, ErrModelLayout)
	}
	return s, nil
}

// buildServedModel encodes one ServerModel. The returned registers hold the
// whole model, header included.
func buildServedModel(m *ServerModel) (*servedModel, []uint16, error) {
	if m.Registers != nil {
		if m.Values != nil {
			return nil, nil, &EncodeError{ModelID: m.ID, Message: "both Values and Registers are given"}
		}
		if len(m.Registers) > 0xFFFF {
			return nil, nil, fmt.Errorf("model %d: %d data registers: %w", m.ID, len(m.Registers), ErrModelLayout)
		}
		regs := make([]uint16, 2, len(m.Registers)+2)
		regs[0], regs[1] = m.ID, uint16(len(m.Registers))
		return &servedModel{id: m.ID}, append(regs, m.Registers...), nil
	}
	schema := m.Schema
	if schema == nil {
		schema = registry.ByID(m.ID)
	}
	if schema == nil {
		return nil, nil, fmt.Errorf("%w: model %d has no schema in the registry, give its registers", ErrUnknownModel, m.ID)
	}
	if m.ID != 0 && m.ID != schema.ID {
		return nil, nil, &EncodeError{ModelID: m.ID, Message: fmt.Sprintf("the schema given is that of model %d", schema.ID)}
	}
	values := m.Values.Clone()
	regs, slots, err := encodeModel(schema, values)
	if err != nil {
		return nil, nil, err
	}
	return &servedModel{id: schema.ID, schema: schema, values: values, slots: slots}, regs, nil
}

// UnitID returns the Modbus unit ID the device answers.
func (s *Server) UnitID() uint8 { return s.unitID }

// BaseAddress returns the register address of the "SunS" marker.
func (s *Server) BaseAddress() uint16 { return s.base }

// RegType returns the register type the device map is served in.
func (s *Server) RegType() modbus.RegType { return s.regType }

// Models returns where each model sits in the device map, in chain order,
// without the end marker: what a client's discovery finds. The position of a
// model in the result is the index the other methods take.
func (s *Server) Models() []ModelHeader {
	out := make([]ModelHeader, len(s.models))
	for i, m := range s.models {
		start := int(s.base) + m.start
		out[i] = ModelHeader{
			ID:           m.id,
			Length:       uint16(m.length - 2),
			StartAddress: uint16(start),
			EndAddress:   uint16(start + m.length - 1),
			NextAddress:  uint16(start + m.length),
		}
	}
	return out
}

// model returns the model at index, or an error matching ErrUnknownModel.
func (s *Server) model(index int) (*servedModel, error) {
	if index < 0 || index >= len(s.models) {
		return nil, fmt.Errorf("%w: the server has no model at index %d (%d models)", ErrUnknownModel, index, len(s.models))
	}
	return s.models[index], nil
}

// registers returns the model's registers inside the image. The caller holds
// the lock.
func (s *Server) registers(m *servedModel) []uint16 {
	return s.image[m.start : m.start+m.length]
}

// Model returns the model at index of the chain as it is served right now,
// decoded the way a client reads it; client writes that were accepted are
// visible in it. The result is a snapshot that the caller owns.
//
// A model served from raw registers yields a DecodedModel holding only the
// data registers in RawRegisters and a warning, as Device.ReadModel does for
// a model without schema. Model returns an error matching ErrUnknownModel
// for an index outside the chain.
func (s *Server) Model(index int) (*DecodedModel, error) {
	m, err := s.model(index)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	regs := append([]uint16(nil), s.registers(m)...)
	s.mu.RUnlock()

	addr := s.base + uint16(m.start)
	if m.schema == nil {
		return &DecodedModel{
			ModelID:         m.id,
			Name:            fmt.Sprintf("unknown_%d", m.id),
			InstanceAddress: addr,
			RawRegisters:    regs[2:],
			Warnings:        []string{"no schema available, returning raw registers only"},
		}, nil
	}
	return DecodeModel(regs, m.schema, addr)
}

// Update changes the values of the model at index of the chain in one atomic
// step. fn receives a copy of the model's current values, including what
// clients wrote, and modifies it; when fn returns nil the model is encoded
// from the result and replaces the served registers.
//
// Nothing changes when Update returns an error: the error fn returned, the
// error of EncodeModel for values that cannot be encoded, an error matching
// ErrModelLayout when the new values would change the model's length (a
// different number of group instances), ErrUnknownModel for an index outside
// the chain, or ErrEncode for a model served from raw registers (use
// SetRegisters).
//
// fn runs while the Server is locked: it must not call methods of the Server.
func (s *Server) Update(index int, fn func(v *Values) error) error {
	m, err := s.model(index)
	if err != nil {
		return err
	}
	if m.schema == nil {
		return &EncodeError{ModelID: m.id, Message: "the model is served from raw registers and has no values; use SetRegisters"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	values := m.values.Clone()
	if values == nil {
		values = &Values{}
	}
	if fn != nil {
		if err := fn(values); err != nil {
			return err
		}
	}
	regs, slots, err := encodeModel(m.schema, values)
	if err != nil {
		return err
	}
	if len(regs) != m.length {
		return fmt.Errorf("sunspec: server: model %d would change from %d to %d registers: %w",
			m.id, m.length-2, len(regs)-2, ErrModelLayout)
	}
	copy(s.registers(m), regs)
	m.values, m.slots = values, slots
	return nil
}

// SetValues replaces all values of the model at index of the chain: points
// that values does not hold become not implemented. It is Update with a
// function that discards the current values, and returns the same errors.
// values is copied.
func (s *Server) SetValues(index int, values *Values) error {
	values = values.Clone()
	return s.Update(index, func(v *Values) error {
		*v = Values{}
		if values != nil {
			*v = *values
		}
		return nil
	})
}

// SetPoint sets one point of the top-level group of the model at index of
// the chain, leaving every other value as it is. A nil value makes the point
// not implemented. Use Update to change points of nested groups or several
// points at once. It returns the errors of Update.
func (s *Server) SetPoint(index int, name string, value any) error {
	return s.Update(index, func(v *Values) error {
		v.Set(name, value)
		return nil
	})
}

// SetRegisters replaces the data registers (what follows the ID and length
// registers) of the model at index of the chain. It works for every model.
// For a model served from values, those values are replaced by what the new
// registers decode to, and the registers must describe the same group
// structure as before: the same counts, nothing left over.
//
// Nothing changes when SetRegisters returns an error: one matching
// ErrModelLayout when len(data) is not the model's length or the registers
// would change the group structure, or ErrUnknownModel for an index outside
// the chain. data is copied.
func (s *Server) SetRegisters(index int, data []uint16) error {
	m, err := s.model(index)
	if err != nil {
		return err
	}
	if len(data) != m.length-2 {
		return fmt.Errorf("sunspec: server: model %d has %d data registers, %d given: %w",
			m.id, m.length-2, len(data), ErrModelLayout)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.schema == nil {
		copy(s.registers(m)[2:], data)
		return nil
	}

	regs := make([]uint16, m.length)
	copy(regs, s.registers(m)[:2])
	copy(regs[2:], data)
	dm, decErr := DecodeModel(regs, m.schema, 0)
	values := dm.Values()
	_, slots, err := encodeModel(m.schema, values)
	if decErr != nil || err != nil || !sameLayout(slots, m.slots) {
		return fmt.Errorf("sunspec: server: model %d: the registers describe a different group structure: %w", m.id, ErrModelLayout)
	}
	copy(s.registers(m), regs)
	m.values = values
	return nil
}

// sameLayout reports whether two encodings place the same points at the same
// offsets.
func sameLayout(a, b []pointSlot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].offset != b[i].offset || a[i].meta != b[i].meta {
			return false
		}
	}
	return true
}

// HandleCoils rejects every coil request with modbus.ErrIllegalFunction: a
// SunSpec device map has no coils.
func (s *Server) HandleCoils(_ context.Context, _ *modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

// HandleDiscreteInputs rejects every discrete input request with
// modbus.ErrIllegalFunction.
func (s *Server) HandleDiscreteInputs(_ context.Context, _ *modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

// HandleHoldingRegisters serves reads and writes of holding registers as
// described on Server. A nil request is answered with
// modbus.ErrServerDeviceFailure.
func (s *Server) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if req == nil {
		return nil, modbus.ErrServerDeviceFailure
	}
	if req.UnitID != s.unitID {
		return nil, modbus.ErrGWTargetFailedToRespond
	}
	if s.regType != modbus.HoldingRegister {
		return nil, modbus.ErrIllegalDataAddress
	}
	if req.IsWrite {
		return nil, s.write(ctx, req)
	}
	return s.read(req.Addr, req.Quantity)
}

// HandleInputRegisters serves reads of input registers when the Server was
// created with RegType modbus.InputRegister; otherwise every address is
// answered with modbus.ErrIllegalDataAddress. A nil request is answered with
// modbus.ErrServerDeviceFailure.
func (s *Server) HandleInputRegisters(_ context.Context, req *modbus.InputRegistersRequest) ([]uint16, error) {
	if req == nil {
		return nil, modbus.ErrServerDeviceFailure
	}
	if req.UnitID != s.unitID {
		return nil, modbus.ErrGWTargetFailedToRespond
	}
	if s.regType != modbus.InputRegister {
		return nil, modbus.ErrIllegalDataAddress
	}
	return s.read(req.Addr, req.Quantity)
}

// span returns the image indexes [lo, hi) of a register range, or ok false
// when the range is empty or not wholly inside the device map.
func (s *Server) span(addr, quantity uint16) (lo, hi int, ok bool) {
	lo = int(addr) - int(s.base)
	hi = lo + int(quantity)
	return lo, hi, quantity > 0 && lo >= 0 && hi <= len(s.image)
}

func (s *Server) read(addr, quantity uint16) ([]uint16, error) {
	lo, hi, ok := s.span(addr, quantity)
	if !ok {
		return nil, modbus.ErrIllegalDataAddress
	}
	out := make([]uint16, hi-lo)
	s.mu.RLock()
	copy(out, s.image[lo:hi])
	s.mu.RUnlock()
	return out, nil
}

// write applies a client write, or returns the Modbus exception that refuses
// it.
func (s *Server) write(ctx context.Context, req *modbus.HoldingRegistersRequest) error {
	lo, hi, ok := s.span(req.Addr, uint16(len(req.Args)))
	if !ok || len(req.Args) > 0xFFFF {
		return modbus.ErrIllegalDataAddress
	}
	// A valid write lies inside the data of one model: anything longer
	// crosses a header.
	index := -1
	for i, m := range s.models {
		if lo >= m.start+2 && hi <= m.start+m.length {
			index = i
			break
		}
	}
	if index < 0 || s.models[index].schema == nil {
		return modbus.ErrIllegalDataAddress
	}
	m := s.models[index]

	s.mu.Lock()
	defer s.mu.Unlock()

	// The write must cover whole writable points and nothing else.
	covered := 0
	for i := range m.slots {
		sl := &m.slots[i]
		from, to := m.start+sl.offset, m.start+sl.offset+sl.meta.Size
		if to <= lo || from >= hi {
			continue
		}
		if !sl.writable || from < lo || to > hi {
			return modbus.ErrIllegalDataAddress
		}
		covered += sl.meta.Size
	}
	if covered != hi-lo {
		return modbus.ErrIllegalDataAddress
	}

	regs := append([]uint16(nil), s.registers(m)...)
	copy(regs[lo-m.start:], req.Args)
	// The model keeps its length and its counts, so it decodes as before.
	dm, _ := DecodeModel(regs, m.schema, s.base+uint16(m.start))
	if s.onWrite != nil {
		w := &ServerWrite{
			ClientAddr: req.ClientAddr,
			ModelIndex: index,
			ModelID:    m.id,
			Address:    req.Addr,
			Registers:  append([]uint16(nil), req.Args...),
			Model:      dm,
		}
		dm.Group.Walk(func(g *DecodedGroup) {
			for i := range g.Points {
				if off := m.start + g.Points[i].RegisterOffset; off >= lo && off < hi {
					w.Points = append(w.Points, g.Points[i])
				}
			}
		})
		if err := s.onWrite(ctx, w); err != nil {
			return err
		}
	}
	copy(s.registers(m), regs)
	m.values = dm.Values()
	return nil
}

// serveShutdownTimeout bounds how long Serve waits for connections to end.
const serveShutdownTimeout = 5 * time.Second

// Serve listens as conf says and serves the device until ctx is done, then
// shuts the listener and its connections down and returns nil. It is a
// convenience for
//
//	ms, err := modbus.NewServer(conf, s)
//	err = ms.Start()
//	<-ctx.Done()
//	err = ms.Shutdown(...)
//
// and the one place where this package opens a listener; use those calls
// directly to control the lifecycle yourself. conf.URL selects the transport
// (go-modbus serves "tcp://host:port" and "tcp+tls://host:port"); conf also
// carries the idle timeout, client limit, TLS material, logger and metrics.
//
// Serve returns the go-modbus error when conf is invalid or the address
// cannot be listened on, and the shutdown error when connections do not end
// within five seconds.
func (s *Server) Serve(ctx context.Context, conf *modbus.ServerConfig) error {
	ms, err := modbus.NewServer(conf, s)
	if err != nil {
		return err
	}
	if err := ms.Start(); err != nil {
		return err
	}
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
	defer cancel()
	return ms.Shutdown(sctx)
}
