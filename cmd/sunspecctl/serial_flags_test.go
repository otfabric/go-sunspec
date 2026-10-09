// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/otfabric/go-modbus"
)

// parseFlags binds a fresh command tree and parses args, leaving the result in
// the global flag variables.
func parseFlags(t *testing.T, args ...string) {
	t.Helper()
	root := newRootCmd()
	if err := root.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags(%v): %v", args, err)
	}
}

func TestClientConfig_SerialFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want modbus.Config
	}{
		{
			name: "defaults leave the line settings to go-modbus",
			args: []string{"--url", "rtu:///dev/ttyUSB0"},
			want: modbus.Config{URL: "rtu:///dev/ttyUSB0", Parity: modbus.ParityNone},
		},
		{
			name: "9600 8E1",
			args: []string{"--url", "rtu:///dev/ttyUSB0", "--baud", "9600", "--data-bits", "8", "--parity", "even", "--stop-bits", "1"},
			want: modbus.Config{URL: "rtu:///dev/ttyUSB0", Speed: 9600, DataBits: 8, Parity: modbus.ParityEven, StopBits: 1},
		},
		{
			name: "odd parity, short form and upper case",
			args: []string{"--url", "ascii://COM3", "--baud", "19200", "--parity", "O"},
			want: modbus.Config{URL: "ascii://COM3", Speed: 19200, Parity: modbus.ParityOdd},
		},
		{
			name: "parity spelled out in mixed case with spaces",
			args: []string{"--parity", " None "},
			want: modbus.Config{URL: "tcp://localhost:502", Parity: modbus.ParityNone},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parseFlags(t, tc.args...)
			got, err := clientConfig()
			if err != nil {
				t.Fatalf("clientConfig: %v", err)
			}
			if got.URL != tc.want.URL || got.Speed != tc.want.Speed || got.DataBits != tc.want.DataBits ||
				got.Parity != tc.want.Parity || got.StopBits != tc.want.StopBits {
				t.Errorf("got url=%q speed=%d data=%d parity=%v stop=%d, want url=%q speed=%d data=%d parity=%v stop=%d",
					got.URL, got.Speed, got.DataBits, got.Parity, got.StopBits,
					tc.want.URL, tc.want.Speed, tc.want.DataBits, tc.want.Parity, tc.want.StopBits)
			}
			if got.Timeout != flagTimeout || got.Logger == nil {
				t.Errorf("timeout or logger not set: %+v", got)
			}
			// The configuration must be one go-modbus accepts.
			if err := modbus.ValidateConfig(got); err != nil {
				t.Errorf("go-modbus rejects the configuration: %v", err)
			}
		})
	}
}

// Invalid serial settings are rejected before anything is opened.
func TestSerialFlags_InvalidValuesRejected(t *testing.T) {
	cases := map[string][]string{
		"invalid --parity":    {"--parity", "mark"},
		"invalid --data-bits": {"--data-bits", "9"},
		"invalid --stop-bits": {"--stop-bits", "3"},
	}
	for want, flags := range cases {
		t.Run(want, func(t *testing.T) {
			args := append([]string{"detect", "--url", "rtu:///nonexistent/sunspecctl-test-port"}, flags...)
			res := runCLI(t, args...)
			if res.err == nil || !strings.Contains(res.err.Error(), want) {
				t.Fatalf("want an error containing %q, got %v", want, res.err)
			}
			if strings.Contains(res.err.Error(), "connect") {
				t.Errorf("the port was opened before the flags were validated: %v", res.err)
			}
		})
	}
}

// A serial URL reaches go-modbus with the line settings: the failure is the
// missing device, not the configuration.
func TestSerialFlags_SerialURLIsOpened(t *testing.T) {
	res := runCLI(t, "detect", "--url", "rtu:///nonexistent/sunspecctl-test-port", "--baud", "9600", "--parity", "even")
	if res.err == nil || !strings.Contains(res.err.Error(), "connect") {
		t.Fatalf("want a connect error for a missing serial device, got %v", res.err)
	}
}

func TestSerialFlags_InHelp(t *testing.T) {
	res := runCLI(t, "--help")
	for _, flag := range []string{"--baud", "--data-bits", "--parity", "--stop-bits"} {
		if !strings.Contains(res.stdout, flag) {
			t.Errorf("help does not mention %s:\n%s", flag, res.stdout)
		}
	}
}
