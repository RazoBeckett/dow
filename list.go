package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var (
		filters    []string
		sortOrder  string
		limit      int
		watchMode  bool
		idFilter   string
		prettyMode bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all downloads",
		Long: `Show every download dow has tracked — past and present.

By default the output is plain key/value blocks — minimal and token-efficient
for use inside coding agents. Pass --pretty for an aligned, coloured table.

Combine --filter, --sort, --limit, and --watch to slice the list any way you want.

Valid filter values: downloading, complete, cancelled, failed, paused
Valid sort values:   asc (oldest first, default), desc (newest first)`,
		Example: `  # Everything (plain, token-efficient):
  dow list

  # Pretty aligned table:
  dow list --pretty

  # Look up a specific download by ID:
  dow list --id=3f9a2c

  # Only what's currently running:
  dow list --filter=downloading

  # Watch live (refreshes every second):
  dow list -w
  dow list --pretty --watch --filter=downloading

  # Downloading and cancelled together:
  dow list --filter=downloading,cancelled

  # Last cancelled entry:
  dow list --filter=cancelled --sort=desc --limit=1

  # Five oldest failures:
  dow list --filter=failed --sort=asc --limit=5`,

		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if watchMode {
				return runListWatch(cmd.Context(), cmd, idFilter, filters, sortOrder, limit, prettyMode)
			}
			return runList(cmd, idFilter, filters, sortOrder, limit, prettyMode)
		},
	}

	cmd.Flags().StringVar(&idFilter, "id", "",
		"Show only the download with this ID")
	cmd.Flags().StringSliceVar(&filters, "filter", nil,
		"Comma-separated statuses to include: downloading, complete, cancelled, failed, paused")
	cmd.Flags().StringVar(&sortOrder, "sort", "asc",
		"Sort order: asc (oldest first) or desc (newest first)")
	cmd.Flags().IntVar(&limit, "limit", 0,
		"Maximum rows to show (0 = no limit)")
	cmd.Flags().BoolVarP(&watchMode, "watch", "w", false,
		"Refresh the list every second (Ctrl+C to stop)")
	cmd.Flags().BoolVarP(&prettyMode, "pretty", "p", false,
		"Aligned, coloured table instead of plain key/value output")

	return cmd
}

func runListWatch(ctx context.Context, cmd *cobra.Command, idFilter string, filters []string, sortOrder string, limit int, pretty bool) error {
	const interval = time.Second

	render := func() error {
		fmt.Print("\033[H\033[2J")
		if pretty {
			hint := ""
			if idFilter != "" {
				hint = "  " + colDim.Render("--id="+idFilter)
			} else if len(filters) > 0 {
				hint = "  " + colDim.Render("--filter="+strings.Join(filters, ","))
			}
			fmt.Printf("  %s%s  %s\n",
				colBold.Render("dow list --watch"),
				hint,
				colDim.Render("· "+time.Now().Format("15:04:05")+"  Ctrl+C to stop"),
			)
		} else {
			hint := ""
			if idFilter != "" {
				hint = " --id=" + idFilter
			} else if len(filters) > 0 {
				hint = " --filter=" + strings.Join(filters, ",")
			}
			fmt.Printf("dow list --watch%s · %s · Ctrl+C to stop\n",
				hint, time.Now().Format("15:04:05"))
		}
		return runList(cmd, idFilter, filters, sortOrder, limit, pretty)
	}

	if err := render(); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println()
			return nil
		case <-ticker.C:
			if err := render(); err != nil {
				return err
			}
		}
	}
}

func runList(cmd *cobra.Command, idFilter string, filters []string, sortOrder string, limit int, pretty bool) error {
	s, err := readState()
	if err != nil {
		return err
	}

	// Work on a copy so we don't mutate the loaded state.
	downloads := make([]Download, len(s.Downloads))
	copy(downloads, s.Downloads)

	out := func(format string, a ...any) {
		cmd.Println(fmt.Sprintf(format, a...))
	}

	// ── ID filter (takes priority over --filter/--sort/--limit) ───────────────
	if idFilter != "" {
		for _, d := range downloads {
			if d.ID == idFilter {
				fmt.Println()
				if pretty {
					printTable([]Download{d}, out)
				} else {
					printListPlain([]Download{d}, out)
				}
				fmt.Println()
				return nil
			}
		}
		if pretty {
			cmd.Println(colRed.Render("  no download found with id " + colBold.Render(idFilter)))
		} else {
			cmd.Println("no download found with id " + idFilter)
		}
		return nil
	}

	// ── Filter ────────────────────────────────────────────────────────────────
	if len(filters) > 0 {
		allowed := make(map[string]bool, len(filters))
		for _, f := range filters {
			f = strings.TrimSpace(strings.ToLower(f))
			if f != "" {
				allowed[f] = true
			}
		}
		var kept []Download
		for _, d := range downloads {
			if allowed[string(d.Status)] {
				kept = append(kept, d)
			}
		}
		downloads = kept
	}

	// ── Sort ──────────────────────────────────────────────────────────────────
	if strings.ToLower(strings.TrimSpace(sortOrder)) == "desc" {
		slices.Reverse(downloads)
	}

	// ── Limit ─────────────────────────────────────────────────────────────────
	if limit > 0 && len(downloads) > limit {
		downloads = downloads[:limit]
	}

	// ── Render ────────────────────────────────────────────────────────────────
	if len(downloads) == 0 {
		if pretty {
			if len(filters) > 0 {
				cmd.Println(colGray.Render("  no downloads match the given filter."))
			} else {
				cmd.Println(colGray.Render("  no downloads yet.  Run 'dow <url>' to start one."))
			}
		} else {
			if len(filters) > 0 {
				cmd.Println("no downloads match the given filter.")
			} else {
				cmd.Println("no downloads yet.")
			}
		}
		return nil
	}

	fmt.Println()
	if pretty {
		printTable(downloads, out)
	} else {
		printListPlain(downloads, out)
	}
	fmt.Println()

	return nil
}
