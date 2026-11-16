//go:build linux

package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	testSwaySocket   = "/run/user/1000/sway-ipc.1000.4242.sock"
	swayFocusOK      = `[{"success":true}]`
	swayFocusRefused = `[{"success":false,"error":"No matching node."}]`
)

// swayTreeFixture is a get_tree reply cut down to what matters here: the hidden
// __i3 output that holds the scratchpad, tiling windows inside nested split
// containers that carry no pid, floating windows, and one process (5000) that
// owns two windows. The titles are synthetic.
const swayTreeFixture = `{
  "id": 1, "name": "root", "type": "root", "focused": false,
  "nodes": [
    {
      "id": 2, "name": "__i3", "type": "output", "layout": "output",
      "nodes": [
        {
          "id": 3, "name": "__i3_scratch", "type": "workspace",
          "nodes": [],
          "floating_nodes": [
            {"id": 30, "name": "scratch-shell", "type": "floating_con", "app_id": "Alacritty", "pid": 7000, "nodes": [], "floating_nodes": []}
          ]
        }
      ],
      "floating_nodes": []
    },
    {
      "id": 4, "name": "OUT-1", "type": "output", "layout": "output",
      "nodes": [
        {
          "id": 5, "name": "1", "type": "workspace",
          "nodes": [
            {
              "id": 6, "name": null, "type": "con", "layout": "splith", "app_id": null,
              "nodes": [
                {"id": 10, "name": "build-pipeline", "type": "con", "app_id": "Alacritty", "pid": 4001, "focused": true, "nodes": [], "floating_nodes": []},
                {
                  "id": 7, "name": null, "type": "con", "layout": "splitv",
                  "nodes": [
                    {"id": 11, "name": "notes", "type": "con", "app_id": "Alacritty", "pid": 4002, "nodes": [], "floating_nodes": []},
                    {"id": 12, "name": "scratch-buffer", "type": "con", "app_id": "Alacritty", "pid": 4003, "nodes": [], "floating_nodes": []}
                  ],
                  "floating_nodes": []
                }
              ],
              "floating_nodes": []
            }
          ],
          "floating_nodes": [
            {"id": 13, "name": "floating-shell", "type": "floating_con", "app_id": "Alacritty", "pid": 4004, "nodes": [], "floating_nodes": []}
          ]
        },
        {
          "id": 8, "name": "2", "type": "workspace",
          "nodes": [
            {"id": 14, "name": "docs - editor", "type": "con", "app_id": "editor", "pid": 5000, "nodes": [], "floating_nodes": []},
            {"id": 15, "name": "tests - editor", "type": "con", "app_id": "editor", "pid": 5000, "nodes": [], "floating_nodes": []}
          ],
          "floating_nodes": []
        }
      ],
      "floating_nodes": []
    }
  ],
  "floating_nodes": []
}`

// swayFixtureViews is what swayTreeFixture holds, in tree order: the tiling
// children of a node come before its floating ones.
var swayFixtureViews = []swayView{
	{ID: 30, PID: 7000, Name: "scratch-shell"},
	{ID: 10, PID: 4001, Name: "build-pipeline"},
	{ID: 11, PID: 4002, Name: "notes"},
	{ID: 12, PID: 4003, Name: "scratch-buffer"},
	{ID: 13, PID: 4004, Name: "floating-shell"},
	{ID: 14, PID: 5000, Name: "docs - editor"},
	{ID: 15, PID: 5000, Name: "tests - editor"},
}

func TestParseSwayViews(t *testing.T) {
	got, err := parseSwayViews([]byte(swayTreeFixture))
	if err != nil {
		t.Fatalf("parseSwayViews() error = %v", err)
	}
	if !reflect.DeepEqual(got, swayFixtureViews) {
		t.Errorf("parseSwayViews() = %+v, want %+v", got, swayFixtureViews)
	}
}

// Containers, workspaces and outputs have no pid, and a window whose owner Sway
// does not know has none either, so none of them can be matched to a process.
func TestParseSwayViews_SkipsNodesWithoutAPID(t *testing.T) {
	tree := `{"id":1,"name":"root","nodes":[
		{"id":2,"name":"OUT-1","nodes":[{"id":3,"name":"1","nodes":[],"floating_nodes":[]}]},
		{"id":4,"name":"unknown-owner","pid":0},
		{"id":5,"name":"invalid-owner","pid":-1}
	]}`

	got, err := parseSwayViews([]byte(tree))
	if err != nil {
		t.Fatalf("parseSwayViews() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("parseSwayViews() = %+v, want no windows", got)
	}
}

func TestParseSwayViews_Invalid(t *testing.T) {
	for _, tt := range []struct{ name, data string }{
		{"empty", ""},
		{"not json", "Unable to connect to the compositor"},
		{"truncated", `{"id":1,"nodes":[`},
		{"array instead of a tree", `[{"id":1,"name":"root"}]`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := parseSwayViews([]byte(tt.data)); err == nil {
				t.Errorf("parseSwayViews(%q) = %+v, want an error", tt.data, got)
			}
		})
	}
}

func TestPickSwayView(t *testing.T) {
	tests := []struct {
		name  string
		hints FocusHints
		want  int64 // con_id of the chosen window; 0 when none is chosen
	}{
		{"the only window of the only ancestor", FocusHints{AncestorPIDs: []int{4001}}, 10},
		{"skips ancestors that own no window", FocusHints{AncestorPIDs: []int{900, 901, 4002}}, 11},
		{"nearest ancestor wins when two own windows", FocusHints{AncestorPIDs: []int{4002, 4001}}, 11},
		{"the ancestor order decides, not the tree order", FocusHints{AncestorPIDs: []int{4001, 4002}}, 10},
		{"floating window", FocusHints{AncestorPIDs: []int{4004}}, 13},
		{"scratchpad window", FocusHints{AncestorPIDs: []int{7000}}, 30},
		{"shared process: the title naming the folder wins", FocusHints{AncestorPIDs: []int{5000}, FolderName: "tests"}, 15},
		{"shared process: the first title naming the folder wins", FocusHints{AncestorPIDs: []int{5000}, FolderName: "editor"}, 14},
		{"shared process: exact title beats two matching folders", FocusHints{AncestorPIDs: []int{5000}, FolderName: "editor", WindowTitle: "tests - editor"}, 15},
		{"shared process: exact title wins with no folder match", FocusHints{AncestorPIDs: []int{5000}, FolderName: "other", WindowTitle: "tests - editor"}, 15},
		{"shared process: unmatched exact title falls back to folder", FocusHints{AncestorPIDs: []int{5000}, FolderName: "tests", WindowTitle: "absent"}, 15},
		{"exact title in a more distant process cannot override nearest ancestor", FocusHints{AncestorPIDs: []int{4002, 5000}, WindowTitle: "tests - editor"}, 11},
		{"shared process: no folder takes the first window", FocusHints{AncestorPIDs: []int{5000}}, 14},
		{"shared process: no title naming the folder takes the first window", FocusHints{AncestorPIDs: []int{5000}, FolderName: "other"}, 14},
		{"a title in another process does not count", FocusHints{AncestorPIDs: []int{5000}, FolderName: "notes"}, 14},
		{"a title in a farther process does not beat the nearest owner", FocusHints{AncestorPIDs: []int{5000, 4001}, FolderName: "build"}, 14},
		{"no ancestor owns a window", FocusHints{AncestorPIDs: []int{900, 901}}, 0},
		{"no ancestors", FocusHints{FolderName: "notes"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := pickSwayView(swayFixtureViews, tt.hints)
			if ok != (tt.want != 0) || got.ID != tt.want {
				t.Errorf("pickSwayView() = (%+v, %v), want con_id %d", got, ok, tt.want)
			}
		})
	}
}

// One IDE process owns a window per project, so the title has to pick the
// project, by the same strict rule the kdotool and xdotool routes use: a plain
// substring would let the project "agent" match "agent-notifications".
func TestPickSwayView_JetBrainsTitleIsStrict(t *testing.T) {
	views := []swayView{
		{ID: 1, PID: 300, Name: "agent-notifications – README.md"},
		{ID: 2, PID: 300, Name: "agent – main.go"},
	}

	for _, tt := range []struct {
		terminal string
		want     int64
	}{
		{"jetbrains-goland", 2},
		{"konsole", 1},
	} {
		hints := FocusHints{TerminalName: tt.terminal, FolderName: "agent", AncestorPIDs: []int{300}}
		if got, ok := pickSwayView(views, hints); !ok || got.ID != tt.want {
			t.Errorf("terminal %q: pickSwayView() = (%+v, %v), want con_id %d", tt.terminal, got, ok, tt.want)
		}
	}
}

func TestProcessAncestorPIDs(t *testing.T) {
	tests := []struct {
		name      string
		start     int
		processes []fakeProcess
		want      []int
	}{
		{
			name:  "chain up to init, which is left out",
			start: 100,
			processes: []fakeProcess{
				{pid: 100, ppid: 101, comm: "sh"},
				{pid: 101, ppid: 102, comm: "claude"},
				{pid: 102, ppid: 103, comm: "bash"},
				{pid: 103, ppid: 1, comm: "alacritty"},
				{pid: 1, ppid: 0, comm: "init"},
			},
			want: []int{100, 101, 102, 103},
		},
		{
			name:  "parent cycle",
			start: 100,
			processes: []fakeProcess{
				{pid: 100, ppid: 101, comm: "sh"},
				{pid: 101, ppid: 100, comm: "sh"},
			},
			want: []int{100, 101},
		},
		{
			name:      "process that is its own parent",
			start:     100,
			processes: []fakeProcess{{pid: 100, ppid: 100, comm: "sh"}},
			want:      []int{100},
		},
		{
			name:  "a PID is listed when its child names it, although its stat is missing",
			start: 100,
			processes: []fakeProcess{
				{pid: 100, ppid: 101, comm: "sh"},
				{pid: 101, ppid: 102, comm: "claude"},
			},
			want: []int{100, 101, 102},
		},
		{
			name:  "the parent is listed although its stat is missing",
			start: 100,
			want:  []int{100},
		},
		{
			name:  "comm with spaces and parentheses",
			start: 100,
			processes: []fakeProcess{
				{pid: 100, ppid: 101, comm: "my (odd) tool"},
				{pid: 101, ppid: 102, comm: "a b) c ("},
				{pid: 102, ppid: 103, comm: "x) S 7"},
				{pid: 103, ppid: 1, comm: "alacritty"},
			},
			want: []int{100, 101, 102, 103},
		},
		{
			name:      "parent is init",
			start:     1,
			processes: []fakeProcess{{pid: 1, ppid: 0, comm: "init"}},
		},
		{
			name:  "no parent",
			start: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFakeProc(t, tt.start, tt.processes...)

			if got := processAncestorPIDs(); !slices.Equal(got, tt.want) {
				t.Errorf("processAncestorPIDs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcessAncestorPIDs_StopsAtTheLimit(t *testing.T) {
	var processes []fakeProcess
	for pid := 100; pid < 100+maxProcessAncestors+8; pid++ {
		processes = append(processes, fakeProcess{pid: pid, ppid: pid + 1, comm: "sh"})
	}
	useFakeProc(t, 100, processes...)

	got := processAncestorPIDs()
	if len(got) != maxProcessAncestors || got[0] != 100 || got[len(got)-1] != 100+maxProcessAncestors-1 {
		t.Errorf("processAncestorPIDs() = %v, want the first %d ancestors starting at 100", got, maxProcessAncestors)
	}
}

func TestGetSwayFocusHints(t *testing.T) {
	useFakeProc(t, 100,
		fakeProcess{pid: 100, ppid: 101, comm: "claude"},
		fakeProcess{pid: 101, ppid: 102, comm: "bash"},
		fakeProcess{pid: 102, ppid: 1, comm: "alacritty"},
	)
	walked := false
	fakeParent := getParentPID
	getParentPID = func() int { walked = true; return fakeParent() }
	t.Cleanup(func() { getParentPID = fakeParent })

	tests := []struct {
		name       string
		env        string
		wantSocket string
		wantPIDs   []int
	}{
		{"outside Sway", "", "", nil},
		{"blank socket", " \n", "", nil},
		{"under Sway", testSwaySocket, testSwaySocket, []int{100, 101, 102}},
		{"socket is trimmed", " " + testSwaySocket + "\n", testSwaySocket, []int{100, 101, 102}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SWAYSOCK", tt.env)
			walked = false

			socket, pids := GetSwayFocusHints()
			if socket != tt.wantSocket || !slices.Equal(pids, tt.wantPIDs) {
				t.Errorf("GetSwayFocusHints() = (%q, %v), want (%q, %v)", socket, pids, tt.wantSocket, tt.wantPIDs)
			}
			if tt.wantSocket == "" && walked {
				t.Error("GetSwayFocusHints() walked the process tree outside Sway")
			}
		})
	}
}

// fakeSwaymsg is a stand-in swaymsg. It answers `-t get_tree` with tree, and any
// other call, a command, with reply. Each call's argv goes to the log.
type fakeSwaymsg struct {
	tree        string
	treeStatus  int
	treeStderr  string
	reply       string
	replyStatus int
	replyStderr string
	hang        bool // spin instead of answering
}

// swaymsgCallEnd closes one call's argv in the log, one argument per line.
const swaymsgCallEnd = "<<end>>"

// useFakeSwaymsg makes the fake the whole of PATH, so nothing real can be
// reached from a test: neither the user's swaymsg nor their wlrctl. The script
// uses shell builtins only, since nothing else is on PATH. It returns that
// directory, for tests that add more fakes, and the log of swaymsg calls.
func useFakeSwaymsg(t *testing.T, fake fakeSwaymsg) (dir, log string) {
	t.Helper()
	for _, text := range []string{fake.tree, fake.treeStderr, fake.reply, fake.replyStderr} {
		if strings.Contains(text, "'") {
			t.Fatalf("a fake swaymsg cannot print a single quote: %q", text)
		}
	}

	// emit prints text, which is left out when empty, on stdout or, with
	// redirect set to ">&2", on stderr.
	emit := func(text, redirect string) string {
		if text == "" {
			return ""
		}
		return fmt.Sprintf("printf '%%s\\n' '%s' %s\n", text, redirect)
	}
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&script, "{ for arg in \"$@\"; do printf '%%s\\n' \"$arg\"; done; echo '%s'; } >> \"$SWAYMSG_LOG\"\n", swaymsgCallEnd)
	if fake.hang {
		script.WriteString("while :; do :; done\n")
	}
	script.WriteString("case \" $* \" in\n*\" get_tree \"*)\n")
	script.WriteString(emit(fake.tree, ""))
	script.WriteString(emit(fake.treeStderr, ">&2"))
	fmt.Fprintf(&script, "exit %d ;;\nesac\n", fake.treeStatus)
	script.WriteString(emit(fake.reply, ""))
	script.WriteString(emit(fake.replyStderr, ">&2"))
	fmt.Fprintf(&script, "exit %d\n", fake.replyStatus)

	dir = t.TempDir()
	writeFakeTool(t, dir, "swaymsg", script.String())
	t.Setenv("PATH", dir)
	log = filepath.Join(t.TempDir(), "swaymsg.log")
	t.Setenv("SWAYMSG_LOG", log)
	return dir, log
}

func writeFakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// readSwaymsgCalls returns the argv of every swaymsg call, in order.
func readSwaymsgCalls(t *testing.T, log string) [][]string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	var call []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == swaymsgCallEnd {
			calls = append(calls, call)
			call = nil
			continue
		}
		call = append(call, line)
	}
	return calls
}

func swayTreeCall() []string {
	return []string{"-s", testSwaySocket, "-t", "get_tree", "-r"}
}

func swayFocusCall(conID int) []string {
	return []string{"-s", testSwaySocket, fmt.Sprintf("[con_id=%d]", conID), "focus"}
}

func TestTrySway(t *testing.T) {
	hints := func(folder string, ancestors ...int) FocusHints {
		return FocusHints{TerminalName: "alacritty", FolderName: folder, SwaySocket: testSwaySocket, AncestorPIDs: ancestors}
	}
	healthy := fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusOK}

	tests := []struct {
		name      string
		fake      fakeSwaymsg
		hints     FocusHints
		wantCalls [][]string
		wantErr   string // a substring of the error; empty means success
	}{
		{"focuses the window of the nearest ancestor", healthy, hints("", 900, 4002, 4001),
			[][]string{swayTreeCall(), swayFocusCall(11)}, ""},
		{"focuses a floating window", healthy, hints("", 4004),
			[][]string{swayTreeCall(), swayFocusCall(13)}, ""},
		{"focuses a scratchpad window", healthy, hints("", 7000),
			[][]string{swayTreeCall(), swayFocusCall(30)}, ""},
		{"one process with several windows: the title naming the folder wins", healthy, hints("tests", 5000),
			[][]string{swayTreeCall(), swayFocusCall(15)}, ""},
		{"one process with several windows: otherwise the first", healthy, hints("other", 5000),
			[][]string{swayTreeCall(), swayFocusCall(14)}, ""},
		{"no ancestor owns a window", healthy, hints("", 900, 901),
			[][]string{swayTreeCall()}, "no sway window"},
		{"sway refuses the focus command", fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusRefused}, hints("", 4001),
			[][]string{swayTreeCall(), swayFocusCall(10)}, "No matching node."},
		{"swaymsg also exits non-zero after a refusal", fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusRefused, replyStatus: 2, replyStderr: "swaymsg: command failed"}, hints("", 4001),
			[][]string{swayTreeCall(), swayFocusCall(10)}, "No matching node."},
		{"swaymsg exits non-zero without a reply", fakeSwaymsg{tree: swayTreeFixture, replyStatus: 1, replyStderr: "Unable to connect to the compositor"}, hints("", 4001),
			[][]string{swayTreeCall(), swayFocusCall(10)}, "Unable to connect to the compositor"},
		{"an empty reply with a zero exit status is a success", fakeSwaymsg{tree: swayTreeFixture}, hints("", 4001),
			[][]string{swayTreeCall(), swayFocusCall(10)}, ""},
		{"get_tree fails", fakeSwaymsg{treeStatus: 1, treeStderr: "Unable to connect to the compositor"}, hints("", 4001),
			[][]string{swayTreeCall()}, "Unable to connect to the compositor"},
		{"get_tree does not print a tree", fakeSwaymsg{tree: "not a tree"}, hints("", 4001),
			[][]string{swayTreeCall()}, "unreadable sway tree"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, log := useFakeSwaymsg(t, tt.fake)

			err := trySway(tt.hints)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("trySway() error = %v, want success", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("trySway() error = %v, want one containing %q", err, tt.wantErr)
			}
			if got := readSwaymsgCalls(t, log); !reflect.DeepEqual(got, tt.wantCalls) {
				t.Errorf("swaymsg calls = %q, want %q", got, tt.wantCalls)
			}
		})
	}
}

// Without both the socket and the ancestry there is nothing to look for, so no
// swaymsg may run: the hook was older than this route, or ran outside Sway.
func TestTrySway_ExecsNothingWithoutHints(t *testing.T) {
	for _, tt := range []struct {
		name  string
		hints FocusHints
	}{
		{"no hints", FocusHints{TerminalName: "alacritty", FolderName: "proj"}},
		{"no socket", FocusHints{AncestorPIDs: []int{4001}}},
		{"no ancestors", FocusHints{SwaySocket: testSwaySocket}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, log := useFakeSwaymsg(t, fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusOK})

			if err := trySway(tt.hints); err == nil {
				t.Error("trySway() error = nil, want an error")
			}
			if calls := readSwaymsgCalls(t, log); len(calls) != 0 {
				t.Errorf("swaymsg ran with %q, want no call", calls)
			}
		})
	}
}

func TestTrySway_SwaymsgNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := trySway(FocusHints{SwaySocket: testSwaySocket, AncestorPIDs: []int{4001}})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("trySway() error = %v, want a not installed error", err)
	}
}

// A compositor that stops answering must not wedge the daemon's click handling.
func TestTrySway_HungSwaymsgTimesOut(t *testing.T) {
	_, log := useFakeSwaymsg(t, fakeSwaymsg{hang: true})
	original := swaymsgTimeout
	t.Cleanup(func() { swaymsgTimeout = original })
	swaymsgTimeout = 200 * time.Millisecond

	start := time.Now()
	err := trySway(FocusHints{SwaySocket: testSwaySocket, AncestorPIDs: []int{4001}})

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("trySway() error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("trySway() took %v, want it bounded by the timeout", elapsed)
	}
	if got := readSwaymsgCalls(t, log); !reflect.DeepEqual(got, [][]string{swayTreeCall()}) {
		t.Errorf("swaymsg calls = %q, want only get_tree", got)
	}
}

// useFakeWlrctl adds a wlrctl to dir that logs its argv and succeeds, and
// returns that log.
func useFakeWlrctl(t *testing.T, dir string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "wlrctl.log")
	t.Setenv("WLRCTL_LOG", log)
	writeFakeTool(t, dir, "wlrctl", "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$WLRCTL_LOG\"\n")
	return log
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Xwayland terminals can share an exact title. The originating process must
// win even if wmctrl could successfully focus a different window by title.
func TestTryFocusWithHints_SwayPrecedesDuplicateTitle(t *testing.T) {
	dir, swayLog := useFakeSwaymsg(t, fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusOK})
	wlrctlLog := useFakeWlrctl(t, dir)
	wmctrlLog := filepath.Join(t.TempDir(), "wmctrl.log")
	t.Setenv("WMCTRL_LOG", wmctrlLog)
	writeFakeTool(t, dir, "wmctrl", `#!/bin/sh
printf '%s\n' "$*" >> "$WMCTRL_LOG"
`)

	err := TryFocusWithHints(FocusHints{
		TerminalName: "terminator", FolderName: "proj", WindowTitle: "same-title",
		SwaySocket: testSwaySocket, AncestorPIDs: []int{4002, 4001},
	})
	if err != nil {
		t.Fatalf("TryFocusWithHints() error = %v", err)
	}
	if got, want := readSwaymsgCalls(t, swayLog), [][]string{swayTreeCall(), swayFocusCall(11)}; !reflect.DeepEqual(got, want) {
		t.Errorf("swaymsg calls = %q, want %q", got, want)
	}
	if fileExists(wmctrlLog) || fileExists(wlrctlLog) {
		t.Error("title or wlrctl fallback ran after process-exact Sway focus")
	}
}

// If no window belongs to an ancestor, keep exact-title focus as the fallback,
// rather than forcing wlrctl's broad app_id selection.
func TestTryFocusWithHints_SwayMissFallsBackToTitle(t *testing.T) {
	dir, swayLog := useFakeSwaymsg(t, fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusOK})
	wlrctlLog := useFakeWlrctl(t, dir)
	wmctrlLog := filepath.Join(t.TempDir(), "wmctrl.log")
	t.Setenv("WMCTRL_LOG", wmctrlLog)
	writeFakeTool(t, dir, "wmctrl", `#!/bin/sh
printf '%s\n' "$*" >> "$WMCTRL_LOG"
`)

	err := TryFocusWithHints(FocusHints{
		TerminalName: "terminator", FolderName: "proj", WindowTitle: "same-title",
		SwaySocket: testSwaySocket, AncestorPIDs: []int{900, 901},
	})
	if err != nil {
		t.Fatalf("TryFocusWithHints() error = %v", err)
	}
	if got, want := readSwaymsgCalls(t, swayLog), [][]string{swayTreeCall()}; !reflect.DeepEqual(got, want) {
		t.Errorf("swaymsg calls = %q, want %q (one Sway attempt)", got, want)
	}
	if got, _ := os.ReadFile(wmctrlLog); string(got) != "-F -a same-title\n" {
		t.Errorf("wmctrl ran %q, want exact-title fallback", got)
	}
	if fileExists(wlrctlLog) {
		t.Error("wlrctl ran although exact-title fallback succeeded")
	}
}

// An IDE or terminal server can own several Sway windows. Exact title remains
// useful when multiple windows in that single PID share the project folder.
func TestTryFocusWithHints_SharedPIDPrefersExactTitle(t *testing.T) {
	_, swayLog := useFakeSwaymsg(t, fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusOK})
	err := TryFocusWithHints(FocusHints{
		TerminalName: "terminator", FolderName: "editor", WindowTitle: "tests - editor",
		SwaySocket: testSwaySocket, AncestorPIDs: []int{5000},
	})
	if err != nil {
		t.Fatalf("TryFocusWithHints() error = %v", err)
	}
	if got, want := readSwaymsgCalls(t, swayLog), [][]string{swayTreeCall(), swayFocusCall(15)}; !reflect.DeepEqual(got, want) {
		t.Errorf("swaymsg calls = %q, want %q", got, want)
	}
}

// Sway sits ahead of wlrctl in the chain, and a window it focuses ends the chain,
// so wlrctl cannot activate every window of the same app_id on top of it.
func TestTryFocusWithHints_SwayWindowEndsTheChain(t *testing.T) {
	dir, swayLog := useFakeSwaymsg(t, fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusOK})
	wlrctlLog := useFakeWlrctl(t, dir)

	err := TryFocusWithHints(FocusHints{
		TerminalName: "alacritty", FolderName: "proj",
		SwaySocket: testSwaySocket, AncestorPIDs: []int{4002, 4001},
	})

	if err != nil {
		t.Fatalf("TryFocusWithHints() error = %v", err)
	}
	if got, want := readSwaymsgCalls(t, swayLog), [][]string{swayTreeCall(), swayFocusCall(11)}; !reflect.DeepEqual(got, want) {
		t.Errorf("swaymsg calls = %q, want %q", got, want)
	}
	if fileExists(wlrctlLog) {
		t.Error("wlrctl ran although Sway focused the window")
	}
}

// A session whose ancestry holds no Sway window, such as one inside tmux, is left
// to wlrctl, as before.
func TestTryFocusWithHints_NoSwayWindowFallsBackToWlrctl(t *testing.T) {
	dir, swayLog := useFakeSwaymsg(t, fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusOK})
	wlrctlLog := useFakeWlrctl(t, dir)

	err := TryFocusWithHints(FocusHints{
		TerminalName: "alacritty", FolderName: "proj",
		SwaySocket: testSwaySocket, AncestorPIDs: []int{900, 901},
	})

	if err != nil {
		t.Fatalf("TryFocusWithHints() error = %v", err)
	}
	if got, want := readSwaymsgCalls(t, swayLog), [][]string{swayTreeCall()}; !reflect.DeepEqual(got, want) {
		t.Errorf("swaymsg calls = %q, want %q", got, want)
	}
	if got, _ := os.ReadFile(wlrctlLog); string(got) != "toplevel focus app_id:Alacritty\n" {
		t.Errorf("wlrctl ran %q, want its app_id focus", got)
	}
}

// A hook older than this route sends neither field, which must leave the chain
// exactly as it was.
func TestTryFocusWithHints_HookWithoutSwayHintsGoesToWlrctl(t *testing.T) {
	dir, swayLog := useFakeSwaymsg(t, fakeSwaymsg{tree: swayTreeFixture, reply: swayFocusOK})
	wlrctlLog := useFakeWlrctl(t, dir)

	if err := TryFocusWithHints(FocusHints{TerminalName: "alacritty", FolderName: "proj"}); err != nil {
		t.Fatalf("TryFocusWithHints() error = %v", err)
	}
	if fileExists(swayLog) {
		t.Error("swaymsg ran although the hook captured no Sway session")
	}
	if got, _ := os.ReadFile(wlrctlLog); string(got) != "toplevel focus app_id:Alacritty\n" {
		t.Errorf("wlrctl ran %q, want its app_id focus", got)
	}
}

// roundTripHints sends hints through a real Client and Server over a unix socket
// and returns what the server kept for the click.
func roundTripHints(t *testing.T, hints FocusHints) FocusHints {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "daemon.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	server := &Server{notifier: focusFlowNotifier{}, focusCtx: make(map[uint32]FocusHints)}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		server.wg.Add(1)
		server.handleConnection(conn)
		done <- nil
	}()

	client := &Client{socketPath: listener.Addr().String()}
	if _, err := client.SendNotification("title", "body", "", hints, 30); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not complete request")
	}
	return server.focusCtx[17]
}

// Only the click needs the Sway hints, long after the hook has gone, so they
// have to survive the client, the wire and the server.
func TestFocusFlowCarriesSwayHints(t *testing.T) {
	for _, tt := range []struct {
		name  string
		hints FocusHints
	}{
		{"under Sway", FocusHints{TerminalName: "alacritty", FolderName: "proj", SwaySocket: testSwaySocket, AncestorPIDs: []int{4242, 4200, 4100}}},
		{"hook outside Sway", FocusHints{TerminalName: "alacritty", FolderName: "proj"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := roundTripHints(t, tt.hints); !reflect.DeepEqual(got, tt.hints) {
				t.Errorf("server kept %+v, want %+v", got, tt.hints)
			}
		})
	}
}
