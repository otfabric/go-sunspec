// SPDX-License-Identifier: MIT

package devicefile_test

import (
	"fmt"
	"os"

	"github.com/otfabric/go-sunspec/devicefile"
	"github.com/otfabric/go-sunspec/registry"
)

func ExampleParse() {
	file, err := devicefile.Parse([]byte(`{
	  "unit_id": 1,
	  "base_address": 40000,
	  "models": [
	    {"id": 1, "points": {"Mn": "Acme", "Md": "PV-5000"}},
	    {"id": 103, "points": {"W": 4200, "W_SF": 0, "St": "MPPT"}},
	    {"id": 64900, "registers": [1, 2, 3]}
	  ]
	}`))
	if err != nil {
		panic(err)
	}
	// Building the device validates every value and the layout. Hand the
	// Server to modbus.NewServer, or call its Serve method, to serve it.
	srv, err := file.Server(nil)
	if err != nil {
		panic(err)
	}
	for _, m := range srv.Models() {
		fmt.Println(m.ID, m.StartAddress, m.Length)
	}

	// Errors say where the problem is.
	_, err = devicefile.Parse([]byte("{\n  \"models\": [{\"id\": 1,}]\n}"))
	fmt.Println(err)
	file, _ = devicefile.Parse([]byte(`{"models": [{"id": 103, "points": {"W": true}}]}`))
	_, err = file.Server(nil)
	fmt.Println(err)
	// Output:
	// 1 40002 66
	// 103 40070 50
	// 64900 40122 3
	// line 2, column 23: invalid character '}' looking for beginning of object key string
	// models[0] (model 103): point W: unsupported value true: use a number, a string, a list or null
}

func ExampleTemplate() {
	// A skeleton to fill in: every point null, one instance of each group.
	file := devicefile.New(devicefile.DefaultUnitID, devicefile.DefaultBaseAddress)
	file.Models = append(file.Models, devicefile.Template(registry.ByID(1)))
	if err := file.Write(os.Stdout); err != nil {
		panic(err)
	}
	// Output:
	// {
	//   "unit_id": 1,
	//   "base_address": 40000,
	//   "models": [
	//     {
	//       "id": 1,
	//       "name": "Common",
	//       "points": {
	//         "Mn": null,
	//         "Md": null,
	//         "Opt": null,
	//         "Vr": null,
	//         "SN": null,
	//         "DA": null
	//       }
	//     }
	//   ]
	// }
}
