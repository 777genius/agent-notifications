//go:build linux

package daemon

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

//go:embed xres_window.py
var xresWindowSource string

// xresWindowPID asks the X server for the local client that *owns* an XID.
// _NET_WM_PID is an application-controlled hint and is deliberately ignored.
// A disposable, isolated Python subprocess bounds potentially blocking Xlib
// calls; absence of Python or libXRes 1.2 keeps the notification informational.
func xresWindowPID(ctx context.Context, display, id, mode, nonce string) (int, error) {
	pid, _, err := xresWindowRecord(ctx, display, id, mode, nonce)
	return pid, err
}

// xresWindowRecord returns the verified server PID and, on mark, the
// window-lifetime nonce shared by all notifications from that same window.
func xresWindowRecord(ctx context.Context, display, id, mode, nonce string) (int, string, error) {
	if ctx == nil || ctx.Err() != nil || !localX11Display.MatchString(display) {
		return 0, "", errors.New("unqualified_x11")
	}
	normalized, err := normalizeX11WindowID(id)
	if err != nil || normalized != id || id == "0" {
		return 0, "", errors.New("invalid_x11_id")
	}
	op, cancel := context.WithTimeout(ctx, 900*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(op, "/usr/bin/python3", "-I", "-c", xresWindowSource, mode, display, id, nonce)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "DISPLAY=" + display, "HOME=" + os.Getenv("HOME")}
	if authority := os.Getenv("XAUTHORITY"); authority != "" {
		cmd.Env = append(cmd.Env, "XAUTHORITY="+authority)
	}
	out, err := cmd.Output()
	if err != nil || op.Err() != nil {
		return 0, "", errors.New("xres_owner_unavailable")
	}
	fields := strings.Fields(string(out))
	marker := ""
	if mode == "mark" {
		if len(fields) != 2 || len(fields[1]) != 32 {
			return 0, "", errors.New("xres_marker_invalid")
		}
		if _, err := hex.DecodeString(fields[1]); err != nil {
			return 0, "", errors.New("xres_marker_invalid")
		}
		marker = fields[1]
	} else if len(fields) != 1 {
		return 0, "", errors.New("xres_owner_invalid")
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 1 {
		return 0, "", errors.New("xres_owner_invalid")
	}
	return pid, marker, nil
}

func windowNonce() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}
