// SPDX-License-Identifier: MIT

// Command sunspecctl inspects SunSpec devices over Modbus: it detects the
// SunSpec marker, lists the models a device exposes, and reads or polls
// decoded models and points.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec"
	"github.com/spf13/cobra"
)

// Global flag values. newRootCmd binds them to the root command's persistent
// flags and thereby resets them to their defaults.
var (
	flagURL     string
	flagUnitID  uint8
	flagTimeout time.Duration
	flagJSON    bool
	flagRaw     bool

	// Serial-line settings, used with rtu:// and ascii:// URLs only.
	flagBaud     uint
	flagDataBits uint
	flagParity   string
	flagStopBits uint

	// Build metadata set at build time via -ldflags.
	version   = "dev"
	tag       = ""
	commit    = ""
	buildDate = ""
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// newRootCmd builds the sunspecctl command tree. Commands write their output
// to the command's output and error streams (os.Stdout and os.Stderr unless
// overridden with SetOut and SetErr).
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:               "sunspecctl",
		Short:             "SunSpec Modbus tool for inspecting solar inverters and meters",
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	root.PersistentFlags().StringVar(&flagURL, "url", "tcp://localhost:502", "Modbus device URL")
	root.PersistentFlags().Uint8Var(&flagUnitID, "unit-id", 1, "Modbus unit ID")
	root.PersistentFlags().DurationVar(&flagTimeout, "timeout", 10*time.Second, "Operation timeout")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	root.PersistentFlags().BoolVar(&flagRaw, "raw", false, "Include raw register hex in output")
	root.PersistentFlags().UintVar(&flagBaud, "baud", 0, "Serial baud rate for rtu:// and ascii:// URLs (0 = 19200)")
	root.PersistentFlags().UintVar(&flagDataBits, "data-bits", 0, "Serial data bits, 5-8 (0 = 8 for rtu://, 7 for ascii://)")
	root.PersistentFlags().StringVar(&flagParity, "parity", "none", "Serial parity: none, even or odd")
	root.PersistentFlags().UintVar(&flagStopBits, "stop-bits", 0, "Serial stop bits, 1 or 2 (0 = 2 without parity, 1 with parity)")
	_ = root.RegisterFlagCompletionFunc("parity", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"none", "even", "odd"}, cobra.ShellCompDirectiveNoFileComp
	})

	root.AddCommand(detectCmd(), modelsCmd(), readCmd(), readModelCmd(), readPointCmd(), pollCmd(), pollModelCmd(), pollPointCmd(), completionCmd(root), versionCmd())

	return root
}

// completionCmd returns the command that prints a shell completion script for
// root.
func completionCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: `Generate a shell completion script for sunspecctl.

To load completions:

  # Bash (add to ~/.bashrc for persistence)
  source <(sunspecctl completion bash)

  # Zsh (add to ~/.zshrc for persistence)
  source <(sunspecctl completion zsh)

  # Fish
  sunspecctl completion fish | source

  # PowerShell
  sunspecctl completion powershell | Out-String | Invoke-Expression`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
	}
}

// clientConfig builds the Modbus client configuration from the global flags.
// The serial-line settings only take effect for rtu:// and ascii:// URLs;
// go-modbus fills in the defaults for the ones left at zero.
func clientConfig() (modbus.Config, error) {
	conf := modbus.Config{
		URL:         flagURL,
		Timeout:     flagTimeout,
		DialTimeout: 5 * time.Second,
		Logger:      modbus.NopLogger(),
		Speed:       flagBaud,
		DataBits:    flagDataBits,
		StopBits:    flagStopBits,
	}
	switch strings.ToLower(strings.TrimSpace(flagParity)) {
	case "", "n", "none":
		conf.Parity = modbus.ParityNone
	case "e", "even":
		conf.Parity = modbus.ParityEven
	case "o", "odd":
		conf.Parity = modbus.ParityOdd
	default:
		return conf, fmt.Errorf("invalid --parity %q: use none, even or odd", flagParity)
	}
	if flagDataBits != 0 && (flagDataBits < 5 || flagDataBits > 8) {
		return conf, fmt.Errorf("invalid --data-bits %d: use 5, 6, 7 or 8", flagDataBits)
	}
	if flagStopBits > 2 {
		return conf, fmt.Errorf("invalid --stop-bits %d: use 1 or 2", flagStopBits)
	}
	return conf, nil
}

// newClient creates and opens a Modbus client for the --url flag. The returned
// function closes it.
func newClient() (*modbus.Client, func(), error) {
	conf, err := clientConfig()
	if err != nil {
		return nil, nil, err
	}
	client, err := modbus.New(conf)
	if err != nil {
		return nil, nil, fmt.Errorf("create client: %w", err)
	}
	if err := client.Open(); err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	return client, func() { _ = client.Close() }, nil
}

// discoverOpts returns the discovery options selected by the global flags.
func discoverOpts() *sunspec.DiscoverOptions {
	return &sunspec.DiscoverOptions{UnitID: flagUnitID}
}

// --- version ---

// versionCmd returns the command that prints the build metadata.
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the sunspecctl version",
		Run: func(cmd *cobra.Command, args []string) {
			out := cmd.OutOrStdout()
			if flagJSON {
				info := map[string]string{"version": version}
				if tag != "" {
					info["tag"] = tag
				}
				if commit != "" {
					info["commit"] = commit
				}
				if buildDate != "" {
					info["buildDate"] = buildDate
				}
				_ = printJSON(out, info)
				return
			}
			_, _ = fmt.Fprintf(out, "sunspecctl %s\n", version)
			if tag != "" {
				_, _ = fmt.Fprintf(out, "tag:       %s\n", tag)
			}
			if commit != "" {
				_, _ = fmt.Fprintf(out, "commit:    %s\n", commit)
			}
			if buildDate != "" {
				_, _ = fmt.Fprintf(out, "built:     %s\n", buildDate)
			}
		},
	}
}

// --- detect ---

// detectCmd returns the command that probes for the SunSpec marker.
func detectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Detect SunSpec device presence",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cleanup, err := newClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), flagTimeout)
			defer cancel()

			result, err := sunspec.Detect(ctx, client, discoverOpts())
			if err != nil {
				return fmt.Errorf("detect: %w", err)
			}

			if flagJSON {
				return printJSON(cmd.OutOrStdout(), result)
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Detected:     %v\n", result.Detected)
			_, _ = fmt.Fprintf(out, "Unit ID:      %d\n", result.UnitID)
			_, _ = fmt.Fprintf(out, "Base Address: %d\n", result.BaseAddress)
			_, _ = fmt.Fprintf(out, "Reg Type:     %d\n", result.RegType)
			_, _ = fmt.Fprintf(out, "Marker:       0x%04X 0x%04X\n", result.Marker[0], result.Marker[1])
			_, _ = fmt.Fprintf(out, "Attempts:     %d\n", len(result.Attempts))
			return nil
		},
	}
}

// --- models ---

// modelsCmd returns the command that lists the models a device exposes.
func modelsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List discovered SunSpec models",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cleanup, err := newClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), flagTimeout)
			defer cancel()

			device, err := sunspec.Discover(ctx, client, discoverOpts())
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}

			if flagJSON {
				return printJSON(cmd.OutOrStdout(), device.Discovery)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "ID\tNAME\tSTART\tLENGTH\tSCHEMA")
			for _, m := range device.Discovery.Models {
				schema := "yes"
				if !m.SchemaKnown {
					schema = "no"
				}
				_, _ = fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%s\n",
					m.Header.ID, m.Name, m.Header.StartAddress, m.Header.Length, schema)
			}
			_ = w.Flush()
			return nil
		},
	}
}

// --- read ---

// readCmd returns the command that reads and prints every model once.
func readCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read",
		Short: "Read and decode all models",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cleanup, err := newClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), flagTimeout)
			defer cancel()

			device, err := sunspec.Discover(ctx, client, discoverOpts())
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}

			results, err := device.ReadAll(ctx)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: partial read: %v\n", err)
			}

			if flagJSON {
				return printJSON(cmd.OutOrStdout(), results)
			}

			for _, dm := range results {
				printDecodedModel(cmd.OutOrStdout(), dm)
			}
			return nil
		},
	}
}

// --- read-model ---

// readModelCmd returns the command that reads and prints one model.
func readModelCmd() *cobra.Command {
	var modelID uint16

	cmd := &cobra.Command{
		Use:   "read-model",
		Short: "Read and decode a specific model by ID",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cleanup, err := newClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), flagTimeout)
			defer cancel()

			device, err := sunspec.Discover(ctx, client, discoverOpts())
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}

			dm, err := device.ReadModelByID(ctx, modelID)
			if err != nil {
				return fmt.Errorf("read model %d: %w", modelID, err)
			}

			if flagJSON {
				return printJSON(cmd.OutOrStdout(), dm)
			}

			printDecodedModel(cmd.OutOrStdout(), dm)
			return nil
		},
	}

	cmd.Flags().Uint16Var(&modelID, "id", 0, "Model ID to read (required)")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

// --- read-point ---

// readPointCmd returns the command that reads and prints one point.
func readPointCmd() *cobra.Command {
	var (
		modelID   uint16
		pointName string
	)

	cmd := &cobra.Command{
		Use:   "read-point",
		Short: "Read a single named point from a model",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cleanup, err := newClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), flagTimeout)
			defer cancel()

			device, err := sunspec.Discover(ctx, client, discoverOpts())
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}

			inst := device.ModelByID(modelID)
			if inst == nil {
				return fmt.Errorf("model %d not found", modelID)
			}

			dp, err := device.ReadPoint(ctx, *inst, pointName)
			if err != nil {
				return fmt.Errorf("read point: %w", err)
			}

			if flagJSON {
				return printJSON(cmd.OutOrStdout(), dp)
			}

			printPoint(cmd.OutOrStdout(), dp)
			return nil
		},
	}

	cmd.Flags().Uint16Var(&modelID, "model", 0, "Model ID (required)")
	cmd.Flags().StringVar(&pointName, "point", "", "Point name (required)")
	_ = cmd.MarkFlagRequired("model")
	_ = cmd.MarkFlagRequired("point")
	return cmd
}

// --- output helpers ---

// printJSON writes v to out as indented JSON followed by a newline.
func printJSON(out io.Writer, v interface{}) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printPoint writes a single point as "Key: value" lines. With --raw it also
// prints the point's register offset and count.
func printPoint(out io.Writer, dp *sunspec.DecodedPoint) {
	_, _ = fmt.Fprintf(out, "Point:   %s\n", dp.Name)
	_, _ = fmt.Fprintf(out, "Type:    %s\n", dp.Type)
	_, _ = fmt.Fprintf(out, "Raw:     %v\n", dp.RawValue)
	if dp.ScaledValue != nil {
		_, _ = fmt.Fprintf(out, "Scaled:  %g\n", *dp.ScaledValue)
	}
	if dp.Units != "" {
		_, _ = fmt.Fprintf(out, "Units:   %s\n", dp.Units)
	}
	if len(dp.Symbols) > 0 {
		_, _ = fmt.Fprintf(out, "Symbols: %s\n", strings.Join(dp.Symbols, ", "))
	}
	if flagRaw {
		_, _ = fmt.Fprintf(out, "Offset:  %d\n", dp.RegisterOffset)
		_, _ = fmt.Fprintf(out, "Count:   %d\n", dp.RegisterCount)
	}
}

// printDecodedModel writes a model as one table per group instance, listing
// only the implemented points, followed by its warnings. Nested groups are
// indented under the group that contains them. With --raw it also dumps the
// model's raw registers in hex.
func printDecodedModel(out io.Writer, dm *sunspec.DecodedModel) {
	_, _ = fmt.Fprintf(out, "=== Model %d: %s ===\n", dm.ModelID, dm.Name)

	if dm.Group != nil {
		printGroup(out, "Fixed", dm.Group, "  ")
	}

	if flagRaw && len(dm.RawRegisters) > 0 {
		_, _ = fmt.Fprintf(out, "  Raw registers (%d):", len(dm.RawRegisters))
		for i, r := range dm.RawRegisters {
			if i%16 == 0 {
				_, _ = fmt.Fprintf(out, "\n    %04d:", i)
			}
			_, _ = fmt.Fprintf(out, " %04X", r)
		}
		_, _ = fmt.Fprintln(out)
	}

	for _, w := range dm.Warnings {
		_, _ = fmt.Fprintf(out, "  WARNING: %s\n", w)
	}
	_, _ = fmt.Fprintln(out)
}

// printGroup writes the implemented points of one group instance as an
// aligned table of name, type and value (scaled when a scale factor applies),
// then the instances nested inside it, labelled "name index" and indented one
// level deeper.
func printGroup(out io.Writer, label string, g *sunspec.DecodedGroup, indent string) {
	_, _ = fmt.Fprintf(out, "%s[%s]\n", indent, label)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, p := range g.Points {
		if !p.Implemented {
			continue
		}
		val := fmt.Sprintf("%v", p.RawValue)
		if p.ScaledValue != nil {
			val = fmt.Sprintf("%g", *p.ScaledValue)
		}
		extra := ""
		if p.Units != "" {
			extra = " " + p.Units
		}
		if len(p.Symbols) > 0 {
			extra += " [" + strings.Join(p.Symbols, ", ") + "]"
		}
		_, _ = fmt.Fprintf(w, "%s  %s\t%s\t%s%s\n", indent, p.Name, p.Type, val, extra)
	}
	_ = w.Flush()
	for _, c := range g.Groups {
		printGroup(out, fmt.Sprintf("%s %d", c.Name, c.Index), c, indent+"  ")
	}
}
