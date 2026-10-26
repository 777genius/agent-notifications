// Package installprogress presents shell-owned installer state. It never runs
// installation operations or decides their outcome.
package installprogress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const Capability = "installer-progress-v1"

// The private v1 protocol is newline-terminated client<TAB>phase<TAB>outcome.
// stop<TAB><TAB> ends observation; an unterminated line waits for another append.
// Every displayed string comes from these tables, never from untrusted input.
var clients = map[string]string{
	"claude": "Claude Code", "codex": "Codex CLI", "opencode": "OpenCode",
	"gemini": "Gemini CLI", "qoder": "Qoder", "cursor": "Cursor", "global": "Installer",
}

var phases = map[string]string{
	"pending": "queued", "queued": "queued", "registration": "registration",
	"marketplace": "Checking marketplace", "download": "Downloading files",
	"runtime": "Installing runtime", "verification": "Verifying registration",
	"waiting":        "Waiting for shared settings",
	"runtime-lookup": "runtime", "config": "configuration", "MCP": "MCP",
	"preparation": "preparation", "portable": "portable", "complete": "complete",
	"verified-installer":    "Preparing verified installer",
	"existing-installation": "Checking existing installation",
	"shared-settings":       "Checking shared settings",
	"notification-settings": "Preparing notification settings",
	"notification-tools":    "Configuring agent notification tools",
	"installing-claude":     "Installing Claude notifications",
	"installing-codex":      "Installing Codex notifications",
	"installing-opencode":   "Installing OpenCode notifications",
	"installing-gemini":     "Installing Gemini notifications",
	"installing-qoder":      "Installing Qoder notifications",
}

var outcomes = map[string]string{
	"running": "running", "queued": "queued", "done": "done", "completed": "done",
	"failed": "failed", "partial": "partial", "not_started": "not started",
	"activation_failed":                            "registration saved; activation failed",
	"activation_not_attempted":                     "registration saved; activation not started",
	"activation_timeout":                           "registration saved; activation timed out",
	"activation_canceled":                          "registration saved; activation canceled",
	"activation_deadline":                          "registration saved; activation deadline reached",
	"state_requires_inspection":                    "needs inspection",
	"registration_committed":                       "registration committed (partial)",
	"registration_committed_follow_up_not_started": "registration committed; follow-up not started",
}

type event struct{ client, phase, outcome string }

func parse(line string) (event, bool, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != 3 {
		return event{}, false, errors.New("invalid progress event")
	}
	if fields[0] == "stop" && fields[1] == "" && fields[2] == "" {
		return event{}, true, nil
	}
	_, clientOK := clients[fields[0]]
	_, phaseOK := phases[fields[1]]
	_, outcomeOK := outcomes[fields[2]]
	if !clientOK || !phaseOK || !outcomeOK {
		return event{}, false, errors.New("unsupported progress event")
	}
	return event{fields[0], fields[1], fields[2]}, false, nil
}

// Options controls presentation only. Rich must be selected by the caller after
// checking its output terminal. Width is measured in terminal columns.
type Options struct {
	Output        io.Writer
	Rich          bool
	Width         int
	Interval      time.Duration
	OwnerPID      int
	Transient     bool
	TerminalWidth func() int
	Height        int
	TerminalSize  func() (int, int)
	Activity      string
	ActivityLimit int
	StopFile      string
}

type board struct {
	options Options
	rows    []event
	painted int
	frame   int
	stopped bool
	started map[string]time.Time
	now     func() time.Time
}

func (b *board) update(e event) bool {
	for i, row := range b.rows {
		if row.client == e.client {
			if row == e {
				return false
			}
			b.recordStart(e)
			b.rows[i] = e
			return true
		}
	}
	b.recordStart(e)
	b.rows = append(b.rows, e)
	return true
}

func (b *board) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *board) recordStart(e event) {
	if b.started == nil {
		b.started = map[string]time.Time{}
	}
	b.started[e.client] = b.clock()
}

// Only explicit running events or a matching authoritative legacy installer
// stage establish activity. A saved or uncertain result alone never does.
func (b *board) active(e event) bool {
	if b.stopped {
		return false
	}
	if e.outcome == "running" {
		return true
	}
	if e.outcome != "state_requires_inspection" && e.outcome != "registration_committed" && e.outcome != "registration_committed_follow_up_not_started" {
		return false
	}
	global := b.globalPhase()
	return global == "installing-"+e.client ||
		(e.phase == "config" && (global == "notification-settings" || global == "notification-tools")) ||
		(e.phase == "MCP" && global == "notification-tools")
}

func (b *board) activeLabel(e event) string {
	if e.outcome == "running" {
		switch e.phase {
		case "registration":
			return "Registering notifications"
		case "config", "MCP":
			return "Configuring notifications"
		}
		return phases[e.phase]
	}
	if e.phase == "config" || e.phase == "MCP" {
		return "Configuring notifications"
	}
	return "Registering notifications"
}

func (b *board) elapsed(e event) string {
	client := e.client
	if e.outcome != "running" {
		client = "global"
	}
	start, ok := b.started[client]
	if !ok {
		return ""
	}
	// The journal contains no producer timestamps: this is time observed in
	// the current operation, never an invented total installation duration.
	elapsed := max(time.Duration(0), b.clock().Sub(start)).Truncate(time.Second)
	return fmt.Sprintf(" (%s)", elapsed)
}

func (b *board) running() bool {
	for _, row := range b.rows {
		if row.outcome == "running" {
			return true
		}
	}
	return false
}

func (b *board) globalPhase() string {
	if !b.stopped {
		for _, row := range b.rows {
			if row.client == "global" && row.outcome == "running" {
				return row.phase
			}
		}
	}
	return ""
}

func (b *board) paint() error {
	if !b.options.Rich {
		return nil
	}
	if b.options.TerminalWidth != nil {
		if width := b.options.TerminalWidth(); width > 0 {
			b.options.Width = width
		}
	}
	var out strings.Builder
	if b.painted > 0 {
		fmt.Fprintf(&out, "\x1b[%dA", b.painted)
	}
	cardWidth := min(b.options.Width-1, 96)
	card := cardWidth >= 40
	innerWidth := cardWidth - 4
	lines := []string{clip("  Agent Notifications", b.options.Width)}
	if card {
		title := "─ Agent Notifications "
		lines = []string{"\x1b[36m  ╭" + title + strings.Repeat("─", innerWidth-utf8.RuneCountInString(title)) + "╮\x1b[0m"}
	}
	globalPhase := b.globalPhase()
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}[b.frame%10]
	for _, row := range b.rows {
		if b.stopped && row.client == "global" {
			continue
		}
		mark, color := "○", "\x1b[2m"
		switch row.outcome {
		case "running":
			if !b.stopped {
				mark, color = spinner, "\x1b[36m"
			}
		case "done", "completed":
			mark, color = "✓", "\x1b[32m"
		case "failed":
			mark, color = "✗", "\x1b[31m"
		case "partial", "state_requires_inspection", "registration_committed", "registration_committed_follow_up_not_started":
			mark, color = "!", "\x1b[33m"
		case "not_started":
			mark = "-"
		}
		line := fmt.Sprintf("  %s %-12s  %s: %s", mark, clients[row.client], phases[row.phase], outcomes[row.outcome])
		if b.stopped && row.outcome == "running" {
			line = fmt.Sprintf("  ! %-12s  %s: last observed running", clients[row.client], phases[row.phase])
		}
		if row.client != "global" && b.active(row) {
			color = "\x1b[36m"
			line = fmt.Sprintf("  %s %-12s  %s%s", spinner, clients[row.client], b.activeLabel(row), b.elapsed(row))
		} else if row.client != "global" && globalPhase != "" && (row.outcome == "registration_committed" || row.outcome == "registration_committed_follow_up_not_started") {
			color = "\x1b[2m"
			line = fmt.Sprintf("  ○ %-12s  Registered · follow-up pending", clients[row.client])
		}
		if row.client == "global" && row.outcome == "running" {
			line = fmt.Sprintf("  %s %-12s  %s", mark, clients[row.client], phases[row.phase])
		}
		if row.client != "global" && row.phase == "complete" && (row.outcome == "completed" || row.outcome == "done") {
			line = fmt.Sprintf("  %s %-12s  Installed · restart required", mark, clients[row.client])
		}
		rowLines := []string{line}
		// Put the distinct attempt state first, then the saved registration
		// fact on its own bounded line. A narrow terminal must not clip both
		// timed-out and unstarted clients to the same shared prefix.
		attempt := ""
		switch row.outcome {
		case "activation_timeout":
			attempt, mark, color = "Timed out", "!", "\x1b[33m"
		case "activation_not_attempted":
			attempt, mark, color = "Not started", "-", "\x1b[2m"
		case "activation_canceled":
			attempt, mark, color = "Canceled", "!", "\x1b[33m"
		case "activation_deadline":
			attempt, mark, color = "Deadline reached", "!", "\x1b[33m"
		case "activation_failed":
			attempt, mark, color = "Failed", "!", "\x1b[33m"
		}
		if attempt != "" {
			rowLines = []string{fmt.Sprintf("  %s %s: %s", mark, clients[row.client], attempt), "    Registration saved"}
		}
		for _, line := range rowLines {
			if card {
				text := clip(" "+strings.TrimPrefix(line, "  "), innerWidth+1)
				padding := strings.Repeat(" ", innerWidth-utf8.RuneCountInString(text))
				lines = append(lines, "\x1b[36m  │"+color+text+padding+"\x1b[36m│\x1b[0m")
			} else {
				lines = append(lines, color+clip(line, b.options.Width)+"\x1b[0m")
			}
		}
	}
	if card {
		lines = append(lines, "\x1b[36m  ╰"+strings.Repeat("─", innerWidth)+"╯\x1b[0m")
	}
	for _, line := range lines {
		fmt.Fprintf(&out, "\r\x1b[2K%s\n", line)
	}
	err := writeText(b.options.Output, out.String())
	if err == nil {
		b.painted = len(lines)
		b.frame++
	}
	return err
}

func writeText(output io.Writer, text string) error {
	n, err := io.WriteString(output, text)
	if err == nil && n != len(text) {
		return io.ErrShortWrite
	}
	return err
}

func (b *board) clear() {
	if b.painted == 0 {
		return
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\x1b[%dA", b.painted)
	for range b.painted {
		out.WriteString("\r\x1b[2K\n")
	}
	fmt.Fprintf(&out, "\x1b[%dA\r", b.painted)
	_, _ = io.WriteString(b.options.Output, out.String())
}

func (b *board) finish() error {
	if b.stopped || (b.globalPhase() == "" && !b.running()) {
		return nil
	}
	b.clear()
	b.painted = 0
	b.stopped = true
	return b.paint()
}

func clip(text string, width int) string {
	// All labels are ASCII, apart from the single-column progress marks. Keep a
	// spare column so a terminal never wraps and invalidates cursor accounting.
	limit := width - 1
	if limit < 0 {
		limit = 0
	}
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

// Run observes one existing, regular, append-only event file until stop or
// context cancellation. Reads and pending line storage are bounded. Malformed
// events fail the observer without echoing their contents to the terminal.
func Run(ctx context.Context, path string, options Options) error {
	if options.Output == nil {
		return errors.New("progress output is required")
	}
	if options.Interval <= 0 {
		options.Interval = 80 * time.Millisecond
	}
	if options.Width <= 0 {
		options.Width = 80
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("progress events must be a regular file")
	}
	f, err := openEvents(path)
	if err != nil {
		return errors.New("cannot open progress events")
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("progress events must be a regular file")
	}
	b := board{options: options}
	if options.Rich {
		// Restore the borrowed terminal even if the first frame or later write
		// fails. No stdin reader, raw mode, alternate screen, or terminal probing.
		defer func() { _, _ = io.WriteString(options.Output, "\x1b[0m\x1b[?25h") }()
		if options.Transient {
			defer b.clear()
		} else {
			// Owner death, cancellation and malformed input also end the live
			// projection. Preserve final client uncertainty before restoring the
			// cursor; a failed output still gets a best-effort cleanup attempt.
			defer func() { _ = b.finish() }()
		}
		if err := writeText(options.Output, "\x1b[?25l"); err != nil {
			return err
		}
		if err := b.paint(); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(options.Interval)
	defer ticker.Stop()
	buf := make([]byte, 4096)
	pending := ""
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if options.OwnerPID > 0 && !ownerAlive(options.OwnerPID) {
			return errors.New("progress producer exited")
		}
		info, err := f.Stat()
		if err != nil {
			return errors.New("cannot read progress events")
		}
		if info.Size() < offset {
			return errors.New("progress events were truncated")
		}
		// Limit work per polling cycle even if the producer appends continuously.
		for budget := 0; budget < 32768; budget += len(buf) {
			n, readErr := f.Read(buf)
			offset += int64(n)
			pending += string(buf[:n])
			for {
				end := strings.IndexByte(pending, '\n')
				if end < 0 {
					break
				}
				if end > 256 {
					return errors.New("progress event is too long")
				}
				e, stop, err := parse(pending[:end])
				if err != nil {
					return err
				}
				pending = pending[end+1:]
				if stop {
					if options.Rich && !options.Transient {
						return b.finish()
					}
					return nil
				}
				if b.update(e) {
					if options.Rich {
						err = b.paint()
					} else {
						err = writeText(options.Output, fmt.Sprintf("%s: %s - %s\n", clients[e.client], phases[e.phase], outcomes[e.outcome]))
					}
					if err != nil {
						return err
					}
				}
			}
			if len(pending) > 256 {
				return errors.New("progress event is too long")
			}
			if readErr != nil && readErr != io.EOF {
				return errors.New("cannot read progress events")
			}
			if n == 0 || readErr == io.EOF {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if options.Rich && b.running() {
				if err := b.paint(); err != nil {
					return err
				}
			}
		}
	}
}
