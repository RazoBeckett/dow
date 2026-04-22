package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
)

// errPaused is returned by doDownloadQuiet when a pause signal is received.
var errPaused = errors.New("paused")

func newRootCmd() *cobra.Command {
	var showProgress bool
	var workerID string
	var customPath string

	cmd := &cobra.Command{
		Use:   "dow [url]",
		Short: "A dead-simple file downloader with a persistent queue",
		Long: `dow downloads files from direct URLs and keeps a persistent
record of every download — past, present, and failed.

By default dow starts the download in the background, prints one status line,
and immediately returns your prompt. Use --progress to watch it live instead.`,
		Example: `  # Start a download (fire-and-forget):
  dow https://example.com/file.zip

  # Download to a specific directory:
  dow --path=/tmp https://example.com/file.zip

  # Download to a relative directory:
  dow --path=. https://example.com/file.zip

  # Download with a specific filename (absolute):
  dow --path=~/cat.png https://example.com/photo

  # Download with a specific filename (relative to cwd):
  dow --path=cat.mkv https://example.com/videos/cat-on-table.mkv

  # Download with live progress (holds the terminal):
  dow --progress https://example.com/file.zip

  # Check on a specific download by ID:
  dow list --id=3f9a2c

  # Pause, resume, or cancel by ID:
  dow pause 3f9a2c
  dow resume 3f9a2c
  dow cancel 3f9a2c`,

		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if workerID != "" {
				return runWorker(cmd.Context(), workerID)
			}
			if len(args) == 0 {
				return cmd.Help()
			}
			if showProgress {
				return runDownloadForeground(cmd.Context(), args[0], customPath)
			}
			return runDownloadBackground(args[0], customPath)
		},
	}

	cmd.Flags().BoolVar(&showProgress, "progress", false,
		"Hold the terminal and show live progress")
	cmd.Flags().StringVar(&customPath, "path", "",
		"Download destination: a directory or a file path (default: ~/Downloads)")
	cmd.Flags().StringVar(&workerID, "_worker", "", "")
	_ = cmd.Flags().MarkHidden("_worker")

	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newCancelCmd())
	cmd.AddCommand(newPauseCmd())
	cmd.AddCommand(newResumeCmd())
	return cmd
}

// ── Fire-and-forget mode ──────────────────────────────────────────────────────

func runDownloadBackground(rawURL, customPath string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid URL %q – only http/https are supported", rawURL)
	}

	urlFilename := path.Base(u.Path)
	if urlFilename == "" || urlFilename == "." || urlFilename == "/" {
		urlFilename = "download_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	outputPath, filename, err := resolveOutputPath(customPath, urlFilename)
	if err != nil {
		return err
	}

	// Register record; ID is generated atomically inside withState.
	var rec Download
	if err := withState(func(s *appState) error {
		rec = Download{
			ID:        generateShortID(s.Downloads),
			URL:       rawURL,
			Filename:  filename,
			Path:      outputPath,
			Status:    StatusDownloading,
			Size:      -1,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		s.Downloads = append(s.Downloads, rec)
		return nil
	}); err != nil {
		return err
	}

	if err := spawnWorker(rec.ID); err != nil {
		_ = withState(func(s *appState) error {
			patchDownload(s, rec.ID, func(d *Download) { d.Status = StatusFailed })
			return nil
		})
		return fmt.Errorf("%w\nhint: use --progress to download in the foreground", err)
	}

	fmt.Println()
	out := func(format string, a ...any) { fmt.Println(fmt.Sprintf(format, a...)) }
	printListPlain([]Download{rec}, out)
	fmt.Println()
	return nil
}

// ── Background worker ─────────────────────────────────────────────────────────

func runWorker(ctx context.Context, id string) error {
	s, err := readState()
	if err != nil {
		return err
	}
	var rec Download
	var found bool
	for _, d := range s.Downloads {
		if d.ID == id {
			rec = d
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("worker: download %q not found", id)
	}

	// startByte > 0 means this is a resume — pick up where we left off.
	startByte := rec.Downloaded

	dlErr := doDownloadQuiet(ctx, id, rec.URL, rec.Filename, rec.Path, startByte)

	finalStatus := StatusComplete
	switch {
	case dlErr == nil:
	case errors.Is(dlErr, errPaused):
		finalStatus = StatusPaused
	case isCtxErr(dlErr):
		finalStatus = StatusCancelled
	default:
		finalStatus = StatusFailed
	}

	_ = withState(func(s *appState) error {
		patchDownload(s, id, func(d *Download) {
			d.Status = finalStatus
			d.CancelRequested = false
			d.PauseRequested = false
			if finalStatus == StatusComplete {
				d.Progress = 100
				d.Speed = 0
			}
		})
		return nil
	})

	if dlErr != nil && !errors.Is(dlErr, errPaused) && !isCtxErr(dlErr) {
		return dlErr
	}
	return nil
}

// doDownloadQuiet downloads silently — no terminal output.
// startByte > 0 triggers an HTTP Range request to resume a partial download.
func doDownloadQuiet(ctx context.Context, id, rawURL, filename, outputPath string, startByte int64) error {
	// Before making the request, verify the partial file actually matches
	// startByte — if not, fall back to a full download from zero.
	if startByte > 0 {
		fi, statErr := os.Stat(outputPath)
		if statErr != nil || fi.Size() != startByte {
			startByte = 0
			_ = withState(func(s *appState) error {
				patchDownload(s, id, func(d *Download) { d.Downloaded = 0 })
				return nil
			})
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if startByte > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startByte))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	// Server ignored our Range header — fall back to a full download.
	if startByte > 0 && resp.StatusCode == http.StatusOK {
		startByte = 0
		_ = withState(func(s *appState) error {
			patchDownload(s, id, func(d *Download) { d.Downloaded = 0 })
			return nil
		})
	}

	// For 206: Content-Length is the remaining bytes, so total = start + remaining.
	var totalSize int64
	if resp.StatusCode == http.StatusPartialContent && resp.ContentLength >= 0 {
		totalSize = startByte + resp.ContentLength
	} else {
		totalSize = resp.ContentLength // -1 if unknown
	}
	_ = withState(func(s *appState) error {
		patchDownload(s, id, func(d *Download) { d.Size = totalSize })
		return nil
	})

	var f *os.File
	if startByte > 0 {
		f, err = os.OpenFile(outputPath, os.O_APPEND|os.O_WRONLY, 0o644)
	} else {
		f, err = os.Create(outputPath)
	}
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 32*1024)
	downloaded := startByte // total bytes on disk including any prior partial data
	sessionStart := time.Now()
	lastSave := time.Now()

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return fmt.Errorf("write: %w", wErr)
			}
			downloaded += int64(n)

			elapsed := time.Since(sessionStart).Seconds()
			speed := 0.0
			if elapsed > 0 {
				// Speed is based on bytes received in this session only.
				speed = float64(downloaded-startByte) / elapsed
			}
			pct := 0.0
			if totalSize > 0 {
				pct = float64(downloaded) / float64(totalSize) * 100
			}

			if time.Since(lastSave) > 500*time.Millisecond {
				lastSave = time.Now()

				var shouldCancel, shouldPause bool
				_ = withState(func(s *appState) error {
					patchDownload(s, id, func(d *Download) {
						shouldCancel = d.CancelRequested
						shouldPause = d.PauseRequested
						// Always persist downloaded bytes so cancel/pause records progress.
						d.Downloaded = downloaded
						if !shouldCancel && !shouldPause {
							d.Progress = pct
							d.Speed = speed
						}
					})
					return nil
				})

				if shouldCancel {
					return context.Canceled
				}
				if shouldPause {
					return errPaused
				}
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read: %w", readErr)
		}
	}

	// Final byte-count flush.
	_ = withState(func(s *appState) error {
		patchDownload(s, id, func(d *Download) { d.Downloaded = downloaded })
		return nil
	})
	return nil
}

// ── spawnWorker ───────────────────────────────────────────────────────────────

// spawnWorker re-execs the current binary as a detached background worker for
// the given download ID. The child runs in its own session (setsid) so it
// keeps going even after the terminal closes.
func spawnWorker(id string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not locate executable: %w", err)
	}
	child := exec.Command(self, "--_worker="+id)
	child.Stdin = nil
	child.Stdout = nil
	child.Stderr = nil
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return fmt.Errorf("could not spawn background worker: %w", err)
	}
	go func() { _ = child.Wait() }()
	return nil
}

// ── Foreground / --progress mode ─────────────────────────────────────────────

func runDownloadForeground(ctx context.Context, rawURL, customPath string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid URL %q – only http/https are supported", rawURL)
	}

	urlFilename := path.Base(u.Path)
	if urlFilename == "" || urlFilename == "." || urlFilename == "/" {
		urlFilename = "download_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	outputPath, filename, err := resolveOutputPath(customPath, urlFilename)
	if err != nil {
		return err
	}

	var rec Download
	if err := withState(func(s *appState) error {
		rec = Download{
			ID:        generateShortID(s.Downloads),
			URL:       rawURL,
			Filename:  filename,
			Path:      outputPath,
			Status:    StatusDownloading,
			Size:      -1,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		s.Downloads = append(s.Downloads, rec)
		return nil
	}); err != nil {
		return err
	}

	dlErr := doDownload(ctx, rec.ID, rawURL, filename, outputPath)

	finalStatus := StatusComplete
	switch {
	case dlErr == nil:
	case isCtxErr(dlErr):
		finalStatus = StatusCancelled
	default:
		finalStatus = StatusFailed
	}

	_ = withState(func(s *appState) error {
		patchDownload(s, rec.ID, func(d *Download) {
			d.Status = finalStatus
			if finalStatus == StatusComplete {
				d.Progress = 100
				d.Speed = 0
			}
		})
		return nil
	})

	if dlErr != nil && !isCtxErr(dlErr) {
		return dlErr
	}
	if dlErr != nil && isCtxErr(dlErr) {
		fmt.Println("\n" + colGray.Render("  cancelled."))
	}
	return nil
}

func doDownload(ctx context.Context, id, rawURL, filename, outputPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	totalSize := resp.ContentLength
	_ = withState(func(s *appState) error {
		patchDownload(s, id, func(d *Download) { d.Size = totalSize })
		return nil
	})

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	sizeHint := ""
	if totalSize > 0 {
		sizeHint = "  " + colDim.Render(formatBytes(totalSize))
	}
	fmt.Printf("\n  %s%s\n", colBold.Render(filename), sizeHint)

	buf := make([]byte, 32*1024)
	var downloaded int64
	start := time.Now()
	lastSave := time.Now()

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return fmt.Errorf("write: %w", wErr)
			}
			downloaded += int64(n)

			elapsed := time.Since(start).Seconds()
			speed := 0.0
			if elapsed > 0 {
				speed = float64(downloaded) / elapsed
			}
			pct := 0.0
			if totalSize > 0 {
				pct = float64(downloaded) / float64(totalSize) * 100
			}

			fmt.Printf("\r%s",
				renderInlineProgress(filename, pct, speed, downloaded, totalSize))

			if time.Since(lastSave) > 500*time.Millisecond {
				lastSave = time.Now()
				_ = withState(func(s *appState) error {
					patchDownload(s, id, func(d *Download) {
						d.Progress = pct
						d.Speed = speed
						d.Downloaded = downloaded
					})
					return nil
				})
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read: %w", readErr)
		}
	}

	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	fmt.Printf("\r  %-28s  %s  %s%s\n",
		truncate(filename, 28),
		doneStyle.Render("✓ complete"),
		formatBytes(downloaded),
		strings.Repeat(" ", 24),
	)
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// resolveOutputPath turns the user-supplied --path value into a concrete
// (outputPath, filename) pair.
//
// Rules:
//   - Empty                          → ~/Downloads/<urlFilename>, collision-safe
//   - Last component has extension   → treated as a file path; parent dir must exist
//   - Existing directory             → <dir>/<urlFilename>, collision-safe
//   - Ends with /                    → treated as directory; must exist
//   - Otherwise (no ext, not a dir)  → error: directory does not exist
//
// ~ and relative paths are expanded/resolved before any checks.
func resolveOutputPath(customPath, urlFilename string) (outputPath, filename string, err error) {
	if customPath == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", fmt.Errorf("home directory: %w", herr)
		}
		dir := filepath.Join(home, "Downloads")
		if merr := os.MkdirAll(dir, 0o755); merr != nil {
			return "", "", fmt.Errorf("create Downloads dir: %w", merr)
		}
		p := uniquePath(dir, urlFilename)
		return p, filepath.Base(p), nil
	}

	// Expand ~ shorthand.
	if customPath == "~" || strings.HasPrefix(customPath, "~/") {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", fmt.Errorf("home directory: %w", herr)
		}
		if customPath == "~" {
			customPath = home
		} else {
			customPath = filepath.Join(home, customPath[2:])
		}
	}

	// Resolve relative paths (e.g. "cat.mkv" → "/cwd/cat.mkv", "." → "/cwd").
	abs, aerr := filepath.Abs(customPath)
	if aerr != nil {
		return "", "", fmt.Errorf("resolve path: %w", aerr)
	}

	// If it already exists and is a directory, use it as the output directory.
	if fi, serr := os.Stat(abs); serr == nil && fi.IsDir() {
		p := uniquePath(abs, urlFilename)
		return p, filepath.Base(p), nil
	}

	// Trailing separator → must be an existing directory.
	if strings.HasSuffix(customPath, string(filepath.Separator)) {
		return "", "", fmt.Errorf("directory %q does not exist", abs)
	}

	// Last component has an extension → treat as an explicit file path.
	if filepath.Ext(filepath.Base(abs)) != "" {
		dir := filepath.Dir(abs)
		fi, serr := os.Stat(dir)
		if os.IsNotExist(serr) || (serr == nil && !fi.IsDir()) {
			return "", "", fmt.Errorf("directory %q does not exist", dir)
		}
		if serr != nil {
			return "", "", fmt.Errorf("stat %q: %w", dir, serr)
		}
		p := uniquePath(dir, filepath.Base(abs))
		return p, filepath.Base(p), nil
	}

	// No extension, not an existing dir → the user likely meant a directory that
	// doesn't exist yet.
	return "", "", fmt.Errorf("directory %q does not exist", abs)
}

func uniquePath(dir, filename string) string {
	p := filepath.Join(dir, filename)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func isCtxErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "context canceled") ||
		strings.Contains(err.Error(), "context deadline exceeded")
}
