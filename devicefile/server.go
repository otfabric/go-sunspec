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

// maxGroupInstances is the most group instances, at every depth together,
// that ServerModels converts for one model: a model has at most 65535 data
// registers, and every instance that is encoded holds at least one of them.
const maxGroupInstances = 0xFFFF

// ServerModels converts the file's models to what sunspec.NewServer takes.
// A model with registers is served from them; any other one needs a schema
// in the registry and is given by its values.
//
// The groups are converted along the model's schema: a nested group the
// schema does not have at that place is an error, found before its instances
// are converted, and so is a model with more than 65535 group instances, at
// every depth together, which no model can hold (it has at most 65535 data
// registers, and every instance encoded holds at least one). Since v0.11.0;
// before, both were found by the encoder, after every instance had been
// converted.
//
// Errors are *ModelError: they say which model, by position and ID, and
// which group instance and point they concern. Only the form of the values
// and the group names are checked here; the values themselves are checked
// against the schemas when the device is built (NewServer, or File.Server).
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
			meta := registry.ByID(id)
			c := &converter{left: maxGroupInstances, modelID: id, top: meta.Group.Name}
			values, err := c.values(&m.Group, meta.Group, nil)
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
	return (&converter{left: -1}).values(g, nil, nil)
}

// converter converts the group instances of one model.
type converter struct {
	// left is the number of group instances that may still be converted;
	// negative for no limit.
	left int
	// modelID and top, the name of the model's top-level group, name the
	// model in the errors of a conversion along its schema.
	modelID uint16
	top     string
}

// encodeError reports a problem of the group structure the way the encoder
// does, so that it matches sunspec.ErrEncode, as it did when the encoder
// found it, as well as ErrInvalid.
func (c *converter) encodeError(at *instance, format string, args ...any) error {
	return &sunspec.EncodeError{ModelID: c.modelID, Group: at.path(c.top), Message: fmt.Sprintf(format, args...)}
}

// instance names a group instance in errors: the instance index (counting
// from 1) of group in the instance parent. The text is built only for an
// error.
type instance struct {
	parent *instance
	group  string
	index  int
}

// String returns the instance's path as the prefix of an error message:
// "group Crv[1]: group Pt[2]: ", or "" for the model's top-level group.
func (in *instance) String() string {
	if in == nil {
		return ""
	}
	return fmt.Sprintf("%sgroup %s[%d]: ", in.parent.String(), in.group, in.index)
}

// path returns the instance's path as sunspec.EncodeError gives it:
// "DERCurve/Crv[2]/Pt[1]", where top names the model's top-level group.
func (in *instance) path(top string) string {
	if in == nil {
		return top
	}
	return fmt.Sprintf("%s/%s[%d]", in.parent.path(top), in.group, in.index)
}

// values converts the group instance g, which at is. meta is the schema of
// its group; nil converts without a schema, with no check of the group
// names.
func (c *converter) values(g *Group, meta *registry.GroupMeta, at *instance) (*sunspec.Values, error) {
	v := &sunspec.Values{}
	for _, e := range g.Points {
		value, err := pointValue(e.Value)
		if err != nil {
			return nil, fmt.Errorf("%spoint %s: %w", at, e.Name, err)
		}
		v.Set(e.Name, value)
	}
	for name, instances := range g.Groups {
		var child *registry.GroupMeta
		if meta != nil {
			if child = meta.Group(name); child == nil {
				return nil, c.encodeError(at, "the group has no nested group %q", name)
			}
		}
		for i, inst := range instances {
			in := &instance{parent: at, group: name, index: i + 1}
			if c.left == 0 {
				return nil, c.encodeError(in, "more than %d group instances, which no model holds: a model has at most %d data registers",
					maxGroupInstances, maxGroupInstances)
			}
			if c.left > 0 {
				c.left--
			}
			if inst == nil {
				inst = &Group{}
			}
			cv, err := c.values(inst, child, in)
			if err != nil {
				return nil, err
			}
			v.Add(name, cv)
		}
	}
	return v, nil
}

// pointValue converts a JSON value to a value the library encodes: a number,
// a string, a list of bitfield symbol names, a list of raw registers, or nil
// for a point that is not implemented.
func pointValue(value any) (any, error) {
	switch v := value.(type) {
	case nil, string, json.Number:
		return v, nil
	case []any:
		if len(v) > maxListLen {
			return nil, fmt.Errorf("a list of %d values: no point holds more than %d registers", len(v), maxListLen)
		}
		// Count first, so that only the slice the value needs is allocated.
		var nNames, nRegs int
		for _, el := range v {
			switch el.(type) {
			case string:
				nNames++
			case json.Number:
				nRegs++
			default:
				return nil, errors.New("a list must hold bitfield symbol names or raw register values")
			}
		}
		if nNames > 0 && nRegs > 0 {
			return nil, errors.New("a list must hold either bitfield symbol names or raw register values, not both")
		}
		if nRegs > 0 {
			regs := make([]uint16, 0, nRegs)
			for _, el := range v {
				e := el.(json.Number)
				n, err := strconv.ParseUint(string(e), 10, 16)
				if err != nil {
					return nil, fmt.Errorf("register value %s is not a whole number from 0 to 65535", e)
				}
				regs = append(regs, uint16(n))
			}
			return regs, nil
		}
		names := make([]string, 0, nNames)
		for _, el := range v {
			names = append(names, el.(string))
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
