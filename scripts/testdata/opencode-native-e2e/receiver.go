//go:build linux

// Private TEST receiver; external submissions counted independently of Go receipts.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

type receiver struct {
	mu      sync.Mutex
	file    *os.File
	control string
	next    uint32
}

func (r *receiver) Notify(app string, replaces uint32, icon, title, body string, actions []string, hints map[string]dbus.Variant, expiry int32) (uint32, *dbus.Error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	silent, ok := hints["suppress-sound"]
	valid := app == "agent-notifications" && replaces == 0 && icon == "" && title == "OpenCode" && len(actions) == 0 && len(hints) == 1 && ok && silent.Value() == true && expiry > 0 && expiry <= 15000
	// Do not persist arbitrary product payloads or user/private identifiers on failure.
	bodies := map[string]bool{"Task completed": true, "OpenCode asked a question": true, "OpenCode requested permission": true, "An error needs your attention": true}
	valid = valid && bodies[body]
	row := map[string]any{"count": r.next, "valid": valid, "silent": ok && silent.Value() == true, "actions": len(actions), "expiry": expiry}
	if valid {
		row["body"] = body
	}
	if err := json.NewEncoder(r.file).Encode(row); err != nil {
		return 0, dbus.MakeFailedError(fmt.Errorf("TEST record failure"))
	}
	if err := r.file.Sync(); err != nil {
		return 0, dbus.MakeFailedError(fmt.Errorf("TEST sync failure"))
	}
	// Signal real IO entry before a bounded receiver-controlled hold. No arbitrary
	// sleep can turn a zero count into settlement/checkpoint/lease proof.
	if err := os.WriteFile(filepath.Join(r.control, "entered"), []byte("entered\n"), 0600); err != nil {
		return 0, dbus.MakeFailedError(fmt.Errorf("TEST entry failure"))
	}
	if _, err := os.Stat(filepath.Join(r.control, "hold")); err == nil {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(r.control, "release")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				return 0, dbus.MakeFailedError(fmt.Errorf("TEST hold expired"))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return r.next, nil
}

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	root, err := filepath.Abs(os.Args[1])
	if err != nil || root != os.Args[1] {
		os.Exit(2)
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil || physical != root {
		os.Exit(2)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".owned-test-root.json"))
	if err != nil {
		os.Exit(2)
	}
	var marker map[string]any
	if json.Unmarshal(raw, &marker) != nil || marker["purpose"] != "TEST installed AN dual native" {
		os.Exit(2)
	}
	control := filepath.Join(root, "receiver-control")
	if err = os.Mkdir(control, 0700); err != nil {
		os.Exit(2)
	}
	file, err := os.OpenFile(filepath.Join(root, "desktop-private.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	defer file.Close()
	conn, err := dbus.Dial(os.Args[2])
	if err != nil {
		os.Exit(2)
	}
	defer conn.Close()
	if conn.Auth(nil) != nil || conn.Hello() != nil {
		os.Exit(2)
	}
	reply, err := conn.RequestName("org.freedesktop.Notifications", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		os.Exit(2)
	}
	if conn.Export(&receiver{file: file, control: control}, "/org/freedesktop/Notifications", "org.freedesktop.Notifications") != nil {
		os.Exit(2)
	}
	if os.WriteFile(filepath.Join(control, "ready"), []byte("ready\n"), 0600) != nil {
		os.Exit(2)
	}
	// Parent owns stdin/close. EOF closes connection; it must await this leader.
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() == "close" {
			break
		}
	}
}
