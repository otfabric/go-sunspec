// SPDX-License-Identifier: MIT

package devicefile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"
)

// errNoID is the problem of a model without "id".
var errNoID = errors.New(`"id" is missing`)

// ServerModels converts the file's models to what sunspec.NewServer takes.
// A model with registers is served from them; any other one needs a schema
// in the registry and is given by its values.
//
// Errors are *ModelError: they say which model, by position and ID, and
// which point they concern. Only the form of the values is checked here; the
// values themselves are checked against the schemas when the device is
// built (NewServer, or File.Server).
func (f *File) ServerModels() ([]sunspec.ServerModel, error) {
	models := make([]sunspec.ServerModel, 0, len(f.Models))
	for i := range f.Models {
		m := &f.Models[i]
		if m.ID == nil {
			return nil, &ModelError{Index: i, Err: errNoID}
		}
		id := *m.ID
		sm := sunspec.ServerModel{ID: id}
		switch {
		case m.Registers != nil:
			if m.Points != nil || m.Groups != nil {
				return nil, &ModelError{Index: i, ModelID: &id, Err: errors.New(`give either "registers" or "points" and "groups", not both`)}
			}
			sm.Registers = m.Registers
		case !registry.Known(id):
			return nil, &ModelError{Index: i, ModelID: &id, Err: errors.New(`no schema is known for this model: give its data as "registers"`)}
		default:
			values, err := m.values("")
			if err != nil {
				return nil, &ModelError{Index: i, ModelID: &id, Err: err}
			}
			sm.Values = values
		}
		models = append(models, sm)
	}
	return models, nil
}

// Values converts the group instance to the values the library encodes: its
// points, and the instances of its nested groups. A nil instance is an empty
// one. Errors name the nested group instance, counting from 1, and the point
// they concern.
func (g *Group) Values() (*sunspec.Values, error) {
	if g == nil {
		return &sunspec.Values{}, nil
	}
	return g.values("")
}

// values converts a group instance of the file. path names the instance in
// errors.
func (g *Group) values(path string) (*sunspec.Values, error) {
	v := &sunspec.Values{}
	for _, e := range g.Points {
		value, err := pointValue(e.Value)
		if err != nil {
			return nil, fmt.Errorf("%spoint %s: %w", path, e.Name, err)
		}
		v.Set(e.Name, value)
	}
	for name, instances := range g.Groups {
		for i, inst := range instances {
			if inst == nil {
				inst = &Group{}
			}
			child, err := inst.values(fmt.Sprintf("%sgroup %s[%d]: ", path, name, i+1))
			if err != nil {
				return nil, err
			}
			v.Add(name, child)
		}
	}
	return v, nil
}

// pointValue converts a JSON value to a value the library encodes: a number,
// a string, a list of bitfield symbol names, a list of raw registers, or nil
// for a point that is not implemented.
func pointValue(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case nil, string, json.Number:
		return v, nil
	case []interface{}:
		names := make([]string, 0, len(v))
		regs := make([]uint16, 0, len(v))
		for _, el := range v {
			switch e := el.(type) {
			case string:
				names = append(names, e)
			case json.Number:
				n, err := strconv.ParseUint(string(e), 10, 16)
				if err != nil {
					return nil, fmt.Errorf("register value %s is not a whole number from 0 to 65535", e)
				}
				regs = append(regs, uint16(n))
			default:
				return nil, errors.New("a list must hold bitfield symbol names or raw register values")
			}
		}
		if len(names) > 0 && len(regs) > 0 {
			return nil, errors.New("a list must hold either bitfield symbol names or raw register values, not both")
		}
		if len(regs) > 0 {
			return regs, nil
		}
		return names, nil
	case bool:
		return nil, fmt.Errorf("unsupported value %v: use a number, a string, a list or null", v)
	default:
		return nil, errors.New("unsupported value: use a number, a string, a list or null, not an object")
	}
}

// ServerOptions configures the device File.Server builds. A nil
// *ServerOptions selects what the file says.
type ServerOptions struct {
	// UnitID, when not nil, replaces the file's unit ID.
	UnitID *uint8
	// BaseAddress, when not nil, replaces the file's base address.
	BaseAddress *uint16
	// SilentToOtherUnits makes the device ignore requests for another unit
	// ID instead of answering them with an exception; see
	// sunspec.ServerOptions.
	SilentToOtherUnits bool
	// RegType is the register type the device map is served in; the zero
	// value is modbus.HoldingRegister.
	RegType modbus.RegType
	// OnWrite, when not nil, is called for every client write the device
	// accepts, before it takes effect, and can refuse it; see
	// sunspec.ServerOptions.
	OnWrite func(ctx context.Context, w *sunspec.ServerWrite) error
}

// Server builds the device the file describes, which validates every value
// and the layout. The device answers the file's unit ID and has its marker
// at the file's base address (DefaultUnitID and DefaultBaseAddress when the
// file does not say), unless opts replaces them.
//
// It returns the errors of ServerModels, an error matching ErrInvalid for
// unit ID 0, which is the Modbus broadcast address, and the errors of
// sunspec.NewServer: *sunspec.EncodeError for a value that does not fit its
// point, sunspec.ErrModelLayout for models that do not fit the address
// space.
func (f *File) Server(opts *ServerOptions) (*sunspec.Server, error) {
	models, err := f.ServerModels()
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &ServerOptions{}
	}
	so := &sunspec.ServerOptions{
		UnitID:             DefaultUnitID,
		BaseAddress:        DefaultBaseAddress,
		SilentToOtherUnits: opts.SilentToOtherUnits,
		RegType:            opts.RegType,
		OnWrite:            opts.OnWrite,
	}
	if f.UnitID != nil {
		so.UnitID = *f.UnitID
	}
	if opts.UnitID != nil {
		so.UnitID = *opts.UnitID
	}
	if so.UnitID == 0 {
		return nil, invalidError("unit ID 0 is the Modbus broadcast address: use 1 to 255")
	}
	if f.BaseAddress != nil {
		so.BaseAddress = *f.BaseAddress
	}
	if opts.BaseAddress != nil {
		so.BaseAddress = *opts.BaseAddress
	}
	return sunspec.NewServer(so, models...)
}
