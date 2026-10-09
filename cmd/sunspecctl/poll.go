// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/otfabric/go-sunspec"
	"github.com/spf13/cobra"
)

// pollLoop runs fn up to count times (0 = infinite), waiting interval between
// the end of one call and the start of the next. Each call gets its own
// context with the --timeout deadline.
//
// The loop ends without error when ctx is cancelled or the process receives
// SIGINT, also when that makes the running call fail. Any other error from fn
// ends the loop and is returned.
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

// pollCmd returns the command that repeatedly reads and prints every model.
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

			out := cmd.OutOrStdout()
			return pollLoop(cmd.Context(), interval, count, func(ctx context.Context, iteration int) error {
				results, err := device.ReadAll(ctx)
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: partial read: %v\n", err)
				}

				if flagJSON {
					return printJSON(out, results)
				}

				_, _ = fmt.Fprintf(out, "--- poll %d @ %s ---\n", iteration, time.Now().Format(time.RFC3339))
				for _, dm := range results {
					printDecodedModel(out, dm)
				}
				return nil
			})
		},
	}

	cmd.Flags().DurationVar(&interval, "interval", 30*time.Second, "Interval between polls (e.g. 5s, 1m, 1h)")
	cmd.Flags().IntVar(&count, "count", 0, "Number of polls (0 = infinite)")
	return cmd
}

// pollModelCmd returns the command that repeatedly reads and prints one model.
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

			out := cmd.OutOrStdout()
			return pollLoop(cmd.Context(), interval, count, func(ctx context.Context, iteration int) error {
				dm, err := device.ReadModelByID(ctx, modelID)
				if err != nil {
					return fmt.Errorf("read model %d: %w", modelID, err)
				}

				if flagJSON {
					return printJSON(out, dm)
				}

				_, _ = fmt.Fprintf(out, "--- poll %d @ %s ---\n", iteration, time.Now().Format(time.RFC3339))
				printDecodedModel(out, dm)
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

// pollPointCmd returns the command that repeatedly reads and prints one point.
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

			out := cmd.OutOrStdout()
			return pollLoop(cmd.Context(), interval, count, func(ctx context.Context, iteration int) error {
				dp, err := device.ReadPoint(ctx, *inst, pointName)
				if err != nil {
					return fmt.Errorf("read point: %w", err)
				}

				if flagJSON {
					return printJSON(out, dp)
				}

				_, _ = fmt.Fprintf(out, "--- poll %d @ %s ---\n", iteration, time.Now().Format(time.RFC3339))
				printPoint(out, dp)
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
