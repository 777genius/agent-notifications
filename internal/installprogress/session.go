package installprogress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const SessionCapability = "installer-progress-session-v1"
const journalLimit = 256 * 1024
const historyLimit = 16

// session is a projection of safe facts. Snapshots correct current state without
// adding actions or changing the identity of the last observed transition.
type session struct {
	rows    board
	history map[string][]event
	last    map[string]event
	lost    map[string]bool
	stopped bool
}

func (s *session) apply(kind string, e event) bool {
	changed := s.rows.update(e)
	order := map[string]int{"claude": 0, "codex": 1, "opencode": 2, "gemini": 3, "cursor": 4, "global": 5}
	sort.SliceStable(s.rows.rows, func(i, j int) bool { return order[s.rows.rows[i].client] < order[s.rows.rows[j].client] })
	if kind != "transition" {
		return changed
	}
	if previous, ok := s.last[e.client]; ok && previous == e {
		return changed
	}
	s.last[e.client] = e
	h := append(s.history[e.client], e)
	if len(h) > historyLimit {
		h = h[len(h)-historyLimit:]
		s.lost[e.client] = true
	}
	s.history[e.client] = h
	return true
}

func parseSession(line string) (string, event, bool, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != 4 {
		return "", event{}, false, errors.New("invalid session event")
	}
	if fields[0] == "stop" && fields[1] == "" && fields[2] == "" && fields[3] == "" {
		return "", event{}, true, nil
	}
	if fields[0] != "snapshot" && fields[0] != "transition" {
		return "", event{}, false, errors.New("unsupported session event")
	}
	e, stop, err := parse(strings.Join(fields[1:], "\t"))
	if stop {
		err = errors.New("invalid session event")
	}
	return fields[0], e, false, err
}

func activityLabel(e event) string {
	switch e.outcome {
	case "activation_timeout":
		return "Activation timed out"
	case "activation_not_attempted":
		return "Activation not started"
	case "activation_canceled":
		return "Activation canceled"
	case "activation_deadline":
		return "Activation deadline reached"
	case "activation_failed":
		return "Activation failed"
	case "registration_committed", "registration_committed_follow_up_not_started":
		if e.phase == "config" {
			return "Configuration phase"
		}
		if e.phase == "MCP" {
			return "Notification tools phase"
		}
		return "Registration saved"
	}
	if e.phase == "complete" && (e.outcome == "completed" || e.outcome == "done") {
		return "Installed; restart required"
	}
	if e.outcome == "state_requires_inspection" {
		switch e.phase {
		case "registration":
			return "Registration phase"
		case "config":
			return "Configuration phase"
		case "MCP":
			return "Notification tools phase"
		}
	}
	return phases[e.phase] + ": " + outcomes[e.outcome]
}

func (s *session) currentLines(width int, frame int) []string {
	lines := []string{}
	global := ""
	if !s.stopped {
		for _, row := range s.rows.rows {
			if row.client == "global" && row.outcome == "running" {
				global = row.phase
			}
		}
	}
	for _, row := range s.rows.rows {
		if row.client == "global" {
			continue
		}
		label, mark := outcomes[row.outcome], "!"
		switch row.outcome {
		case "done", "completed":
			mark = "+"
		case "queued", "not_started":
			mark, label = "-", "Not started"
		case "running":
			mark = "~"
		case "activation_timeout":
			label = "Timed out"
		case "activation_not_attempted":
			mark, label = "-", "Not started"
		case "activation_canceled":
			label = "Canceled"
		case "activation_deadline":
			label = "Deadline reached"
		case "activation_failed":
			label = "Failed"
		case "registration_committed", "registration_committed_follow_up_not_started":
			label = "Registration saved; follow-up pending"
		}
		if row.phase == "complete" && (row.outcome == "done" || row.outcome == "completed") {
			label = "Installed; restart required"
		}
		active := global == "installing-"+row.client || (row.phase == "config" && (global == "notification-settings" || global == "notification-tools") || row.phase == "MCP" && global == "notification-tools")
		if active && (row.outcome == "state_requires_inspection" || row.outcome == "registration_committed" || row.outcome == "registration_committed_follow_up_not_started") {
			mark, label = "~", "Registering notifications"
			if row.phase == "config" || row.phase == "MCP" {
				label = "Configuring notifications"
			}
		}
		if mark == "~" {
			mark = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}[frame%10]
		}
		name := clients[row.client]
		line := fmt.Sprintf("%s %s: %s", mark, name, label)
		// Narrow states use two lines rather than clipping the distinguishing word.
		if utf8.RuneCountInString(line) >= width-1 {
			lines = append(lines, mark+" "+name, "  "+label)
		} else {
			lines = append(lines, line)
		}
		if strings.HasPrefix(row.outcome, "activation_") || active && (row.outcome == "registration_committed" || row.outcome == "registration_committed_follow_up_not_started") {
			lines = append(lines, "  Registration saved")
		}
	}
	return lines
}

func (s *session) counters() string {
	complete, active, queued := 0, 0, 0
	global := s.rows.globalPhase()
	for _, e := range s.rows.rows {
		if e.client == "global" {
			continue
		}
		switch {
		case e.phase == "complete" && (e.outcome == "done" || e.outcome == "completed"):
			complete++
		case !s.stopped && (e.outcome == "running" || global == "installing-"+e.client || (e.phase == "config" && (global == "notification-settings" || global == "notification-tools") || e.phase == "MCP" && global == "notification-tools")):
			active++
		case e.outcome == "queued" || e.outcome == "not_started":
			queued++
		}
	}
	return fmt.Sprintf("Completed %d; active %d; queued %d", complete, active, queued)
}

type sessionView struct {
	s                      *session
	options                Options
	width, height, painted int
	frame                  int
	plain                  bool
}

func (v *sessionView) geometry() bool {
	width, height := v.options.Width, v.options.Height
	if v.options.TerminalSize != nil {
		w, h := v.options.TerminalSize()
		if w > 0 && h > 0 {
			width, height = w, h
		} else {
			width, height = v.width, v.height
		}
	}
	changed := width != v.width || height != v.height
	if changed {
		v.painted = 0
		v.width, v.height = width, height
	}
	return changed
}

func (v *sessionView) plainCurrent() error {
	for _, e := range v.s.rows.rows {
		if e.client == "global" {
			continue
		}
		if err := writeText(v.options.Output, fmt.Sprintf("%s: %s - %s\n", clients[e.client], phases[e.phase], outcomes[e.outcome])); err != nil {
			return err
		}
	}
	return nil
}

func (v *sessionView) paint() error {
	v.geometry()
	if v.plain || !v.options.Rich {
		return nil
	}
	current := v.s.currentLines(v.width, v.frame)
	mandatory := len(current) + 1
	// Preserve full states through append-only fallback when geometry cannot fit.
	for _, line := range current {
		if utf8.RuneCountInString(line) >= v.width-1 {
			mandatory = v.height + 1
		}
	}
	if v.width < 24 || v.height < 3 || mandatory > v.height-1 {
		v.clear()
		v.plain = true
		return v.plainCurrent()
	}
	budget := v.height - 1
	status := []string{"Agent Notifications"}
	card := v.width >= 50 && mandatory+2 <= budget
	if card {
		// Border and padding also consume terminal columns. Recompute narrow
		// status lines against the card interior, then recheck height and width.
		inner := v.s.currentLines(v.width-4, v.frame)
		if len(inner)+2 > budget {
			card = false
		} else {
			for _, line := range inner {
				if utf8.RuneCountInString(line) >= v.width-4 {
					card = false
					break
				}
			}
			if card {
				current = inner
			}
		}
	}
	if card {
		status[0] = "╭─ Agent Notifications " + strings.Repeat("─", v.width-utf8.RuneCountInString("╭─ Agent Notifications ")-2) + "╮"
	}
	for _, line := range current {
		if card {
			line = "│ " + line + strings.Repeat(" ", max(0, v.width-4-utf8.RuneCountInString(line))) + "│"
		}
		status = append(status, line)
	}
	if card {
		status = append(status, "╰"+strings.Repeat("─", v.width-3)+"╯")
	}
	prefix := []string{}
	room := budget - len(status)
	if global := v.s.rows.globalPhase(); !v.s.stopped && global != "" && room > 0 {
		prefix = append(prefix, clip("Stage: "+phases[global], v.width))
		room--
	}
	if room > 0 {
		prefix = append(prefix, clip(v.s.counters(), v.width))
		room--
	}
	history := []string{}
	if v.options.Activity != "off" && room >= 2 {
		title := "Recent activity"
		for client, lost := range v.s.lost {
			if client != "global" && lost {
				title = "Earlier checkpoints omitted"
				break
			}
		}
		history = append(history, title)
		count := v.options.ActivityLimit
		if count == 0 {
			count = 2
			if v.options.Activity == "expanded" {
				count = 4
			}
		}
		for _, row := range v.s.rows.rows {
			if row.client == "global" {
				continue
			}
			h := v.s.history[row.client]
			if len(h) == 0 || len(history) >= room {
				continue
			}
			actions := []string{}
			if v.s.lost[row.client] {
				actions = append(actions, "Earlier checkpoints omitted")
			}
			for _, e := range h[max(0, len(h)-count):] {
				actions = append(actions, activityLabel(e))
			}
			for len(actions) > 1 && utf8.RuneCountInString(clients[row.client]+": "+strings.Join(actions, " · ")) >= v.width-1 {
				actions = actions[1:]
			}
			label := clients[row.client] + ": " + strings.Join(actions, " · ")
			if v.s.lost[row.client] {
				label = "* " + label
			}
			history = append(history, clip(label, v.width))
		}
	}
	if len(history) == 1 {
		history = nil
	}
	if v.options.Activity != "off" && room > len(history) {
		for client, lost := range v.s.lost {
			if client != "global" && lost {
				history = append(history, clip("* Earlier checkpoints omitted", v.width))
				break
			}
		}
		if v.s.lost["global"] && room > len(history) {
			history = append(history, clip("* Earlier installer checkpoints omitted", v.width))
		}
	}
	lines := append(history, prefix...)
	lines = append(lines, status...)
	var out strings.Builder
	old := v.painted
	if old > 0 {
		fmt.Fprintf(&out, "\x1b[%dA", old)
	}
	for _, line := range lines {
		color := "\x1b[36m"
		if strings.Contains(line, "Installed;") {
			color = "\x1b[32m"
		}
		if strings.Contains(line, "Timed out") || strings.Contains(line, "Canceled") || strings.Contains(line, "Failed") || strings.Contains(line, "Deadline reached") || strings.Contains(line, "needs inspection") {
			color = "\x1b[33m"
		}
		fmt.Fprintf(&out, "\r\x1b[2K%s%s\x1b[0m\n", color, line)
	}
	for i := len(lines); i < old; i++ {
		out.WriteString("\r\x1b[2K\n")
	}
	if old > len(lines) {
		fmt.Fprintf(&out, "\x1b[%dA", old-len(lines))
	}
	if err := writeText(v.options.Output, out.String()); err != nil {
		return err
	}
	v.painted = len(lines)
	v.frame++
	return nil
}

func (v *sessionView) clear() {
	// Resize may have reflowed the borrowed region; never seek into old geometry.
	v.geometry()
	if v.painted == 0 {
		return
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\x1b[%dA", v.painted)
	for range v.painted {
		out.WriteString("\r\x1b[2K\n")
	}
	fmt.Fprintf(&out, "\x1b[%dA\r", v.painted)
	_, _ = io.WriteString(v.options.Output, out.String())
	v.painted = 0
}

func (v *sessionView) fullHistory() error {
	if v.options.Activity != "expanded" {
		return nil
	}
	if err := writeText(v.options.Output, "Session activity (safe checkpoints):\n"); err != nil {
		return err
	}
	for _, row := range v.s.rows.rows {
		h := v.s.history[row.client]
		if len(h) == 0 {
			continue
		}
		if v.s.lost[row.client] {
			label := clients[row.client] + ": earlier checkpoints omitted\n"
			if row.client == "global" {
				label = "Earlier installer checkpoints omitted\n"
			}
			if err := writeText(v.options.Output, label); err != nil {
				return err
			}
		}
		for _, e := range h {
			if err := writeText(v.options.Output, clients[e.client]+": "+activityLabel(e)+"\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

func openRegular(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !privateOwner(info) {
		return nil, errors.New("private regular progress file required")
	}
	f, err := openEvents(path)
	if err != nil {
		return nil, errors.New("cannot open progress file")
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = f.Close()
		return nil, errors.New("progress file changed")
	}
	return f, nil
}

// RunSession folds the captured startup prefix before painting one current
// frame. A separate stop control ends this observer, never the install session.
func RunSession(ctx context.Context, path string, options Options) error {
	if options.Output == nil {
		return errors.New("progress output is required")
	}
	if options.Interval <= 0 {
		options.Interval = 80 * time.Millisecond
	}
	if options.Activity == "" {
		options.Activity = "compact"
	}
	if options.Activity != "compact" && options.Activity != "expanded" && options.Activity != "off" || options.ActivityLimit < 0 || options.ActivityLimit > 16 {
		return errors.New("invalid session presentation options")
	}
	f, err := openRegular(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > journalLimit {
		return errors.New("progress journal exceeds budget")
	}
	startup := info.Size()
	var control *os.File
	if options.StopFile != "" {
		control, err = openRegular(options.StopFile)
		if err != nil {
			return err
		}
		defer control.Close()
	}
	s := &session{history: map[string][]event{}, last: map[string]event{}, lost: map[string]bool{}}
	v := &sessionView{s: s, options: options, plain: !options.Rich}
	richBorrowed := options.Rich
	if richBorrowed {
		defer func() { _, _ = io.WriteString(options.Output, "\x1b[0m\x1b[?25h") }()
		if options.Transient {
			defer v.clear()
		}
	}
	pending := ""
	var offset int64
	process := func(data []byte, live bool) error {
		if s.stopped && len(data) > 0 {
			return errors.New("event after session stop")
		}
		pending += string(data)
		for {
			end := strings.IndexByte(pending, '\n')
			if end < 0 {
				break
			}
			if end > 256 {
				return errors.New("progress event is too long")
			}
			kind, e, stop, err := parseSession(pending[:end])
			if err != nil {
				return err
			}
			pending = pending[end+1:]
			if stop {
				s.stopped = true
				s.rows.stopped = true
				if pending != "" {
					return errors.New("event after session stop")
				}
				return nil
			}
			if s.stopped {
				return errors.New("event after session stop")
			}
			changed := s.apply(kind, e)
			if live && changed && kind == "transition" && v.plain {
				label := activityLabel(e)
				if options.Activity == "off" {
					label = phases[e.phase] + " - " + outcomes[e.outcome]
				}
				if err := writeText(options.Output, clients[e.client]+": "+label+"\n"); err != nil {
					return err
				}
			}
		}
		if len(pending) > 256 {
			return errors.New("progress event is too long")
		}
		return nil
	}
	buf := make([]byte, 4096)
	// Capture fixed size: continuous producers cannot delay the first frame.
	for offset < startup {
		n, readErr := f.Read(buf[:min(int64(len(buf)), startup-offset)])
		offset += int64(n)
		if err := process(buf[:n], false); err != nil {
			return err
		}
		if readErr != nil || n == 0 {
			return errors.New("cannot fold progress journal")
		}
	}
	if options.Rich {
		if err := writeText(options.Output, "\x1b[?25l"); err != nil {
			return err
		}
		if err := v.paint(); err != nil {
			return err
		}
	}
	finish := func() error {
		s.stopped = true
		s.rows.stopped = true
		if options.Transient {
			return nil
		}
		if v.plain {
			if err := v.plainCurrent(); err != nil {
				return err
			}
		} else {
			if err := v.paint(); err != nil {
				return err
			}
		}
		return v.fullHistory()
	}
	if s.stopped { // Initial render already uses the final model.
		if options.Transient {
			return nil
		}
		if v.plain && !options.Rich { // A rich-to-plain initial paint already emitted it.
			if err := v.plainCurrent(); err != nil {
				return err
			}
		}
		return v.fullHistory()
	}
	ticker := time.NewTicker(options.Interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if options.OwnerPID > 0 && !ownerAlive(options.OwnerPID) {
			return errors.New("progress producer exited")
		}
		if control != nil {
			var stop [6]byte
			n, err := control.ReadAt(stop[:], 0)
			if err != nil && err != io.EOF {
				return errors.New("cannot read observer control")
			}
			if n > 0 {
				if string(stop[:n]) != "stop\n" {
					return errors.New("invalid observer control")
				}
				return nil
			}
		}
		info, err := f.Stat()
		if err != nil || info.Size() < offset || info.Size() > journalLimit {
			return errors.New("progress journal unavailable")
		}
		changed := false
		for budget := 0; budget < 32768; budget += len(buf) {
			n, readErr := f.Read(buf)
			offset += int64(n)
			if n > 0 {
				changed = true
				if err := process(buf[:n], true); err != nil {
					return err
				}
			}
			if s.stopped {
				return finish()
			}
			if readErr != nil && readErr != io.EOF {
				return errors.New("cannot read progress journal")
			}
			if n == 0 || readErr == io.EOF {
				break
			}
		}
		if !v.plain && options.Rich && (changed || s.rows.running()) {
			if err := v.paint(); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !v.plain && options.Rich {
				geometryChanged := v.geometry()
				if geometryChanged || s.rows.running() {
					if err := v.paint(); err != nil {
						return err
					}
				}
			}
		}
	}
}

// Promotion is trusted static presentation. It never observes installer output.
func Promotion(out io.Writer) error {
	return writeText(out, "\n----------------------------------------\nBoost your productivity\n  Check out Agent Teams AI, a desktop app for AI agent teams\n  with Claude, Codex and more, from the creator of this plugin:\n  https://github.com/777genius/agent-teams-ai\n----------------------------------------\n")
}
