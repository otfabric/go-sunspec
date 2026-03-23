package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/otfabric/go-sunspec"
	"github.com/spf13/cobra"
)

// pollLoop runs fn up to count times (0 = infinite) at the given interval.
// It prints a header before each iteration and respects context cancellation / SIGINT.
func pollLoop(ctx context.Context, interval time.Duration, count int, fn func(ctx context.Context, iteration int) error) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	for i := 1; count == 0 || i <= count; i++ {
		if i > 1 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(interval):
			}
		}

		iterCtx, cancel := context.WithTimeout(ctx, flagTimeout)
		err := fn(iterCtx, i)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
	return nil
}

func pollCmd() *cobra.Command {
	var (
		interval time.Duration
		count    int
	)

	cmd := &cobra.Command{
		Use:   "poll",
		Short: "Repeatedly read and decode all models",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cleanup, err := newClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), flagTimeout)
			device, err := sunspec.Discover(ctx, client, discoverOpts())
			cancel()
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}

			return pollLoop(context.Background(), interval, count, func(ctx context.Context, iteration int) error {
				results, err := device.ReadAll(ctx)
				if err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "warning: partial read: %v\n", err)
				}

				if flagJSON {
					return printJSON(results)
				}

				fmt.Printf("--- poll %d @ %s ---\n", iteration, time.Now().Format(time.RFC3339))
				for _, dm := range results {
					printDecodedModel(dm)
				}
				return nil
			})
		},
	}

	cmd.Flags().DurationVar(&interval, "interval", 30*time.Second, "Interval between polls (e.g. 5s, 1m, 1h)")
	cmd.Flags().IntVar(&count, "count", 0, "Number of polls (0 = infinite)")
	return cmd
}

func pollModelCmd() *cobra.Command {
	var (
		modelID  uint16
		interval time.Duration
		count    int
	)

	cmd := &cobra.Command{
		Use:   "poll-model",
		Short: "Repeatedly read and decode a specific model by ID",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cleanup, err := newClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), flagTimeout)
			device, err := sunspec.Discover(ctx, client, discoverOpts())
			cancel()
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}

			return pollLoop(context.Background(), interval, count, func(ctx context.Context, iteration int) error {
				dm, err := device.ReadModelByID(ctx, modelID)
				if err != nil {
					return fmt.Errorf("read model %d: %w", modelID, err)
				}

				if flagJSON {
					return printJSON(dm)
				}

				fmt.Printf("--- poll %d @ %s ---\n", iteration, time.Now().Format(time.RFC3339))
				printDecodedModel(dm)
				return nil
			})
		},
	}

	cmd.Flags().Uint16Var(&modelID, "id", 0, "Model ID to read (required)")
	cmd.Flags().DurationVar(&interval, "interval", 30*time.Second, "Interval between polls (e.g. 5s, 1m, 1h)")
	cmd.Flags().IntVar(&count, "count", 0, "Number of polls (0 = infinite)")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

func pollPointCmd() *cobra.Command {
	var (
		modelID   uint16
		pointName string
		interval  time.Duration
		count     int
	)

	cmd := &cobra.Command{
		Use:   "poll-point",
		Short: "Repeatedly read a single named point from a model",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cleanup, err := newClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), flagTimeout)
			device, err := sunspec.Discover(ctx, client, discoverOpts())
			cancel()
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}

			inst := device.ModelByID(modelID)
			if inst == nil {
				return fmt.Errorf("model %d not found", modelID)
			}

			return pollLoop(context.Background(), interval, count, func(ctx context.Context, iteration int) error {
				dp, err := device.ReadPoint(ctx, *inst, pointName)
				if err != nil {
					return fmt.Errorf("read point: %w", err)
				}

				if flagJSON {
					return printJSON(dp)
				}

				fmt.Printf("--- poll %d @ %s ---\n", iteration, time.Now().Format(time.RFC3339))
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
			})
		},
	}

	cmd.Flags().Uint16Var(&modelID, "model", 0, "Model ID (required)")
	cmd.Flags().StringVar(&pointName, "point", "", "Point name (required)")
	cmd.Flags().DurationVar(&interval, "interval", 30*time.Second, "Interval between polls (e.g. 5s, 1m, 1h)")
	cmd.Flags().IntVar(&count, "count", 0, "Number of polls (0 = infinite)")
	_ = cmd.MarkFlagRequired("model")
	_ = cmd.MarkFlagRequired("point")
	return cmd
}
