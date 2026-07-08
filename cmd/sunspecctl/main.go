// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec"
	"github.com/spf13/cobra"
)

var (
	flagURL     string
	flagUnitID  uint8
	flagTimeout time.Duration
	flagJSON    bool
	flagRaw     bool

	// Build metadata set at build time via -ldflags.
	version   = "dev"
	tag       = ""
	commit    = ""
	buildDate = ""
)

func main() {
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

	root.AddCommand(detectCmd(), modelsCmd(), readCmd(), readModelCmd(), readPointCmd(), pollCmd(), pollModelCmd(), pollPointCmd(), completionCmd(root), versionCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

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
				return root.GenBashCompletion(os.Stdout)
			case "zsh":
				return root.GenZshCompletion(os.Stdout)
			case "fish":
				return root.GenFishCompletion(os.Stdout, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
	}
}

func newClient() (*modbus.Client, func(), error) {
	conf := modbus.Config{
		URL:         flagURL,
		Timeout:     flagTimeout,
		DialTimeout: 5 * time.Second,
		Logger:      modbus.NopLogger(),
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

func discoverOpts() *sunspec.DiscoverOptions {
	return &sunspec.DiscoverOptions{UnitID: flagUnitID}
}

// --- version ---

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the sunspecctl version",
		Run: func(cmd *cobra.Command, args []string) {
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
				_ = printJSON(info)
				return
			}
			fmt.Printf("sunspecctl %s\n", version)
			if tag != "" {
				fmt.Printf("tag:       %s\n", tag)
			}
			if commit != "" {
				fmt.Printf("commit:    %s\n", commit)
			}
			if buildDate != "" {
				fmt.Printf("built:     %s\n", buildDate)
			}
		},
	}
}

// --- detect ---

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
				return printJSON(result)
			}

			fmt.Printf("Detected:     %v\n", result.Detected)
			fmt.Printf("Unit ID:      %d\n", result.UnitID)
			fmt.Printf("Base Address: %d\n", result.BaseAddress)
			fmt.Printf("Reg Type:     %d\n", result.RegType)
			fmt.Printf("Marker:       0x%04X 0x%04X\n", result.Marker[0], result.Marker[1])
			fmt.Printf("Attempts:     %d\n", len(result.Attempts))
			return nil
		},
	}
}

// --- models ---

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
				return printJSON(device.Discovery)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
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
				fmt.Fprintf(os.Stderr, "warning: partial read: %v\n", err)
			}

			if flagJSON {
				return printJSON(results)
			}

			for _, dm := range results {
				printDecodedModel(dm)
			}
			return nil
		},
	}
}

// --- read-model ---

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
				return printJSON(dm)
			}

			printDecodedModel(dm)
			return nil
		},
	}

	cmd.Flags().Uint16Var(&modelID, "id", 0, "Model ID to read (required)")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

// --- read-point ---

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
				return printJSON(dp)
			}

			fmt.Printf("Point:   %s\n", dp.Name)
			fmt.Printf("Type:    %s\n", dp.Type)
			fmt.Printf("Raw:     %v\n", dp.RawValue)
			if dp.ScaledValue != nil {
				fmt.Printf("Scaled:  %g\n", *dp.ScaledValue)
			}
			if dp.Units != "" {
				fmt.Printf("Units:   %s\n", dp.Units)
			}
			if len(dp.Symbols) > 0 {
				fmt.Printf("Symbols: %s\n", strings.Join(dp.Symbols, ", "))
			}
			if flagRaw {
				fmt.Printf("Offset:  %d\n", dp.RegisterOffset)
				fmt.Printf("Count:   %d\n", dp.RegisterCount)
			}
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

func printJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printDecodedModel(dm *sunspec.DecodedModel) {
	fmt.Printf("=== Model %d: %s ===\n", dm.ModelID, dm.Name)

	if dm.FixedBlock != nil {
		printBlock("Fixed", dm.FixedBlock)
	}

	for i, rb := range dm.RepeatingBlocks {
		printBlock(fmt.Sprintf("Repeating[%d]", i), rb)
	}

	if flagRaw && len(dm.RawRegisters) > 0 {
		fmt.Printf("  Raw registers (%d):", len(dm.RawRegisters))
		for i, r := range dm.RawRegisters {
			if i%16 == 0 {
				fmt.Printf("\n    %04d:", i)
			}
			fmt.Printf(" %04X", r)
		}
		fmt.Println()
	}

	for _, w := range dm.Warnings {
		fmt.Printf("  WARNING: %s\n", w)
	}
	fmt.Println()
}

func printBlock(label string, block *sunspec.DecodedBlock) {
	fmt.Printf("  [%s]\n", label)
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, p := range block.Points {
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
		_, _ = fmt.Fprintf(w, "    %s\t%s\t%s%s\n", p.Name, p.Type, val, extra)
	}
	_ = w.Flush()
}
