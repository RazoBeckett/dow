package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// ── Colour palette ─────────────────────────────────────────────────────────────

var (
	colGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	colYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	colRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	colGray   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	colCyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	colBold   = lipgloss.NewStyle().Bold(true)
	colDim    = lipgloss.NewStyle().Faint(true)
)

// ── Human-readable sizes ───────────────────────────────────────────────────────

// formatSpeed renders bytes-per-second in the most appropriate unit.
func formatSpeed(bps float64) string {
	switch {
	case bps >= 1e9:
		return fmt.Sprintf("%.1f GB/s", bps/1e9)
	case bps >= 1e6:
		return fmt.Sprintf("%.1f MB/s", bps/1e6)
	case bps >= 1e3:
		return fmt.Sprintf("%.1f KB/s", bps/1e3)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}

// formatETA renders a Unix timestamp as a relative duration.
func formatETA(ts int64) string {
	if ts <= 0 {
		return ""
	}
	now := time.Now().Unix()
	remaining := ts - now
	if remaining <= 0 {
		return "< 1m"
	}
	if remaining < 60 {
		return "< 1m"
	}
	if remaining < 3600 {
		return fmt.Sprintf("%dm", remaining/60)
	}
	if remaining < 86400 {
		h := remaining / 3600
		m := (remaining % 3600) / 60
		if m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	}
	d := remaining / 86400
	return fmt.Sprintf("%dd", d)
}

// formatBytes renders a raw byte count in the most appropriate unit.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ── Progress bar ───────────────────────────────────────────────────────────────

const barWidth = 14

// progressBar returns a compact ASCII bar like [████████░░░░░░].
func progressBar(pct float64) string {
	filled := int(pct / 100 * barWidth)
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	return colDim.Render("[") + colYellow.Render(bar) + colDim.Render("]")
}

// ── Status column ──────────────────────────────────────────────────────────────

// statusCell formats the Status column for dow list.
func statusCell(d Download) string {
	switch d.Status {
	case StatusDownloading:
		var pctStr string
		if d.Size > 0 {
			pctStr = fmt.Sprintf("(%d%%)", int(d.Progress))
		} else {
			pctStr = fmt.Sprintf("(%s)", formatBytes(d.Downloaded))
		}
		return colYellow.Render(fmt.Sprintf("downloading...%s %s", pctStr, formatSpeed(d.Speed)))

	case StatusComplete:
		return colGreen.Render("complete")

	case StatusCancelled:
		return colGray.Render("cancelled")

	case StatusFailed:
		return colRed.Render("failed")

	case StatusPaused:
		var pctStr string
		if d.Size > 0 {
			pctStr = fmt.Sprintf(" (%d%%)", int(d.Progress))
		} else if d.Downloaded > 0 {
			pctStr = fmt.Sprintf(" (%s)", formatBytes(d.Downloaded))
		}
		return colCyan.Render("paused" + pctStr)

	default:
		return string(d.Status)
	}
}

// statusCellPlain is like statusCell but without ANSI codes (for width calculation).
func statusCellPlain(d Download) string {
	switch d.Status {
	case StatusDownloading:
		var pctStr string
		if d.Size > 0 {
			pctStr = fmt.Sprintf("(%d%%)", int(d.Progress))
		} else {
			pctStr = fmt.Sprintf("(%s)", formatBytes(d.Downloaded))
		}
		return fmt.Sprintf("downloading...%s %s", pctStr, formatSpeed(d.Speed))
	case StatusComplete:
		return "complete"
	case StatusCancelled:
		return "cancelled"
	case StatusFailed:
		return "failed"
	case StatusPaused:
		var pctStr string
		if d.Size > 0 {
			pctStr = fmt.Sprintf(" (%d%%)", int(d.Progress))
		} else if d.Downloaded > 0 {
			pctStr = fmt.Sprintf(" (%s)", formatBytes(d.Downloaded))
		}
		return "paused" + pctStr
	default:
		return string(d.Status)
	}
}

// ── Inline progress (shown while downloading) ──────────────────────────────────

// renderInlineProgress builds the single-line progress output printed with \r.
func renderInlineProgress(filename string, pct, speed float64, downloaded, total int64) string {
	name := truncate(filename, 28)
	bar := progressBar(pct)
	spd := colYellow.Render(formatSpeed(speed))

	if total > 0 {
		return fmt.Sprintf("  %-28s  %s  %5.1f%%  %s  ",
			name, bar, pct, spd)
	}
	return fmt.Sprintf("  %-28s  %s  %s  %s  ",
		name, bar, formatBytes(downloaded), spd)
}

// ── Plain (token-efficient) output ────────────────────────────────────────────

// printListPlain writes one key: value block per download, separated by a
// blank line. No ANSI codes — designed to be read by agents without wasting
// tokens on escape sequences or alignment padding.
func printListPlain(downloads []Download, out func(string, ...any)) {
	for i, d := range downloads {
		out("id %s", d.ID)
		out("name %s", d.Filename)
		out("status %s", statusCellPlain(d))
		out("eta %s", formatETA(d.ETA))
		out("path %s", d.Path)
		if i < len(downloads)-1 {
			out("") // blank line between entries
		}
	}
}

// ── Table ──────────────────────────────────────────────────────────────────────

// printTable writes a formatted, aligned table of downloads to out.
func printTable(downloads []Download, out func(string, ...any)) {
	type row struct {
		id        string
		name      string
		statusRaw string // plain, for width measurement
		statusFmt string // coloured, for display
		eta       string
		path      string
	}

	rows := make([]row, len(downloads))
	idW, nameW, statusW, etaW, pathW := len("id"), len("name"), len("status"), len("eta"), len("path")

	for i, d := range downloads {
		r := row{
			id:        d.ID,
			name:      d.Filename,
			statusRaw: statusCellPlain(d),
			statusFmt: statusCell(d),
			eta:      formatETA(d.ETA),
			path:      d.Path,
		}
		if len(r.id) > idW {
			idW = len(r.id)
		}
		if len(r.name) > nameW {
			nameW = len(r.name)
		}
		if len(r.statusRaw) > statusW {
			statusW = len(r.statusRaw)
		}
		if len(r.eta) > etaW {
			etaW = len(r.eta)
		}
		if len(r.path) > pathW {
			pathW = len(r.path)
		}
		rows[i] = r
	}

	idSep     := strings.Repeat("─", idW)
	nameSep   := strings.Repeat("─", nameW)
	statusSep := strings.Repeat("─", statusW)
	etaSep    := strings.Repeat("─", etaW)
	pathSep   := strings.Repeat("─", pathW)

	// Header
	out(" %s  │  %s  │  %s  │  %s  │  %s",
		colBold.Render(padRight("id", idW)),
		colBold.Render(padRight("name", nameW)),
		colBold.Render(padRight("status", statusW)),
		colBold.Render(padRight("eta", etaW)),
		colBold.Render("path"),
	)
	out(" %s──┼──%s──┼──%s──┼──%s──┼──%s", idSep, nameSep, statusSep, etaSep, pathSep)

	// Rows – status column needs extra padding because ANSI codes inflate len().
	for _, r := range rows {
		paddedStatus := r.statusFmt + strings.Repeat(" ", max(0, statusW-len(r.statusRaw)))
		etaVal := r.eta
		if etaVal == "" {
			etaVal = "-"
		}
		out(" %-*s  │  %-*s  │  %s  │  %-*s  │  %s",
			idW, colDim.Render(r.id),
			nameW, r.name,
			paddedStatus,
			etaW, etaVal,
			r.path,
		)
	}
}

// ── String utilities ───────────────────────────────────────────────────────────

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
