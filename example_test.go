// SPDX-License-Identifier: MIT

package sunspec_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"
)

func ExampleDecodeModel() {
	meta := registry.ByID(1)
	if meta == nil {
		panic("model 1 schema missing")
	}

	// Minimal Common model registers: ID, L, then data sized to the schema.
	regs := make([]uint16, meta.FixedLength())
	regs[0] = 1
	regs[1] = uint16(meta.FixedLength() - 2)

	dm, err := sunspec.DecodeModel(regs, meta, 40000)
	if err != nil {
		panic(err)
	}

	fmt.Println(dm.ModelID, dm.Name != "", dm.Group != nil)
	// Output:
	// 1 true true
}

func Example_registry() {
	meta := registry.ByID(101)
	fmt.Println(registry.Known(101), meta != nil, registry.Count() > 0)
	all := registry.All()
	_, ok := all[101]
	fmt.Println(ok)
	// Output:
	// true true true
	// true
}

func ExampleEncodeModel() {
	// A three phase inverter producing 4.2 kW at 230.4 V.
	values := (&sunspec.Values{}).
		Set("W", 4200).Set("W_SF", 0).
		Set("PhVphA", 230.4).Set("V_SF", -1).
		Set("St", "MPPT")

	regs, err := sunspec.EncodeModel(registry.ByID(103), values)
	if err != nil {
		panic(err)
	}
	dm, err := sunspec.DecodeModel(regs, registry.ByID(103), 40070)
	if err != nil {
		panic(err)
	}
	v := dm.Point("PhVphA")
	fmt.Println(len(regs), v.RawValue, *v.ScaledValue, dm.Point("St").Symbols, dm.Point("Hz").Implemented)

	// A value that does not fit its point is an error, not a wrapped number.
	_, err = sunspec.EncodeModel(registry.ByID(103), (&sunspec.Values{}).Set("W", 400000).Set("W_SF", 0))
	fmt.Println(err)
	// Output:
	// 52 2304 230.4 [MPPT] false
	// sunspec: encode model 103 group inverter_three_phase point W type int16: 400000 does not fit a signed 16-bit point: sunspec: value out of range
}

func ExampleServer() {
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{BaseAddress: 40000, UnitID: 1},
		sunspec.ServerModel{ID: 1, Values: (&sunspec.Values{}).
			Set("Mn", "Acme").Set("Md", "PV-5000").Set("SN", "A-0001")},
		sunspec.ServerModel{ID: 103, Values: (&sunspec.Values{}).
			Set("W", 4200).Set("W_SF", 0).
			Set("PhVphA", 230.4).Set("V_SF", -1).
			Set("St", "MPPT")},
		// A vendor model the registry does not know, from raw registers.
		sunspec.ServerModel{ID: 64900, Registers: []uint16{1, 2, 3}},
	)
	if err != nil {
		panic(err)
	}
	for _, m := range srv.Models() {
		fmt.Println(m.ID, m.StartAddress, m.Length)
	}

	// Values can change while the device is being read. The first argument
	// is the position of the model in the chain.
	if err := srv.SetPoint(1, "W", 4350); err != nil {
		panic(err)
	}

	// The Server is a go-modbus RequestHandler: hand it to a Modbus server
	// with
	//
	//	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://0.0.0.0:1502"}, srv)
	//	err = ms.Start()
	//
	// Here a request is answered directly instead.
	regs, err := srv.HandleHoldingRegisters(context.Background(), &modbus.HoldingRegistersRequest{
		UnitID: 1, Addr: 40000, Quantity: 4,
	})
	fmt.Printf("%04X %v\n", regs, err)
	// Output:
	// 1 40002 66
	// 103 40070 50
	// 64900 40122 3
	// [5375 6E53 0001 0042] <nil>
}

func ExampleReadFunctionCodes() {
	// A client that can read a SunSpec device and cannot write to it: any
	// function code but 0x03 and 0x04 is refused before it is sent.
	client, err := modbus.New(modbus.Config{
		URL:                  "tcp://192.168.1.100:502",
		AllowedFunctionCodes: sunspec.ReadFunctionCodes(),
	})
	if err != nil {
		panic(err)
	}
	// Nothing is sent here: the client is not even open.
	err = client.WriteRegister(context.Background(), 1, 40100, 1)
	fmt.Println(errors.Is(err, modbus.ErrFunctionCodeNotAllowed))
	for _, fc := range sunspec.ReadFunctionCodes() {
		fmt.Println(fc.String())
	}
	// Output:
	// true
	// Read Holding Registers (0x03)
	// Read Input Registers (0x04)
}
