package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "cancel <id>",
		Short:        "Cancel an in-progress download",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runSetFlag(args[0], func(d *Download) error {
				if d.Status != StatusDownloading {
					return fmt.Errorf("download %q is not in progress (status: %s)", args[0], d.Status)
				}
				d.CancelRequested = true
				return nil
			}, "cancelling %s…")
		},
	}
}

func newPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "pause <id>",
		Short:        "Pause an in-progress download",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runSetFlag(args[0], func(d *Download) error {
				if d.Status != StatusDownloading {
					return fmt.Errorf("download %q is not in progress (status: %s)", args[0], d.Status)
				}
				d.PauseRequested = true
				return nil
			}, "pausing %s…")
		},
	}
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "resume <id>",
		Short:        "Resume a paused download",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runResume(args[0])
		},
	}
}

// runSetFlag is the shared implementation for cancel and pause: it finds the
// download by ID, calls mutateFn (which may return an error to abort), writes
// state, then prints a confirmation line.
func runSetFlag(id string, mutateFn func(*Download) error, msgFmt string) error {
	var found bool
	if err := withState(func(s *appState) error {
		for i := range s.Downloads {
			if s.Downloads[i].ID == id {
				found = true
				return mutateFn(&s.Downloads[i])
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no download found with id %q", id)
	}
	fmt.Printf("  "+msgFmt+"\n", colBold.Render(id))
	return nil
}

// runResume re-spawns the background worker for a paused download.
func runResume(id string) error {
	s, err := readState()
	if err != nil {
		return err
	}
	var rec *Download
	for i := range s.Downloads {
		if s.Downloads[i].ID == id {
			rec = &s.Downloads[i]
			break
		}
	}
	if rec == nil {
		return fmt.Errorf("no download found with id %q", id)
	}
	if rec.Status != StatusPaused {
		return fmt.Errorf("download %q is not paused (status: %s)", id, rec.Status)
	}

	// Mark it downloading again before spawning the worker.
	if err := withState(func(s *appState) error {
		patchDownload(s, id, func(d *Download) {
			d.Status = StatusDownloading
			d.PauseRequested = false
			d.CancelRequested = false
		})
		return nil
	}); err != nil {
		return err
	}

	if err := spawnWorker(id); err != nil {
		// Roll the status back so the record isn't left as "downloading" with no worker.
		_ = withState(func(s *appState) error {
			patchDownload(s, id, func(d *Download) { d.Status = StatusPaused })
			return nil
		})
		return err
	}

	// Print a fresh snapshot so the user can see it's back in flight.
	s2, err := readState()
	if err != nil {
		return err
	}
	for _, d := range s2.Downloads {
		if d.ID == id {
			fmt.Println()
			out := func(format string, a ...any) { fmt.Println(fmt.Sprintf(format, a...)) }
			printTable([]Download{d}, out)
			fmt.Println()
			break
		}
	}
	return nil
}
