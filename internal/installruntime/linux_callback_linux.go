//go:build linux

package installruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/linuxcallback"
	"github.com/777genius/agent-notifications/internal/notification"
)

// StageLinuxCallback computes one transaction's immutable reader, snapshot and
// activation registrations. It never publishes or retires an older installation.
// Commit owns the existing component/config CAS, staging and rollback protocol.
func StageLinuxCallback(ctx context.Context, controlRoot, dataRoot, reader string, s linuxcallback.Snapshot) ([]File, notification.LinuxBinding, error) {
	var binding notification.LinuxBinding
	if !filepath.IsAbs(controlRoot) || !filepath.IsAbs(dataRoot) || s.InstallationID == "" {
		return nil, binding, linuxcallback.ErrUnavailable
	}
	dir := filepath.Join(controlRoot, "linux-callback", s.InstallationID)
	binary, e := readRegularFileLimit(reader, maxManagedFile)
	if e != nil || !bytes.Contains(binary, []byte(linuxcallback.ReaderMarker)) {
		return nil, binding, linuxcallback.ErrUnavailable
	}
	s.Records = filepath.Join(dir, "records")
	s.Reader = filepath.Join(dir, "reader")
	s.ReaderSHA256 = linuxcallback.Digest(binary)
	s.DataRoot = dataRoot
	if e := linuxcallback.CheckSelectedContext(ctx, s); e != nil {
		return nil, binding, e
	}
	payload, e := json.Marshal(s)
	if e != nil {
		return nil, binding, e
	}
	binding = notification.LinuxBinding{SnapshotPath: filepath.Join(dir, "snapshot.json"), SHA256: linuxcallback.Digest(payload)}
	desktop, service, e := linuxcallback.RegistrationBytes(s, binding)
	if e != nil {
		return nil, binding, e
	}
	raw := map[string][]byte{binding.SnapshotPath: payload, s.Reader: binary,
		filepath.Join(dataRoot, "applications", s.ApplicationID+".desktop"):       desktop,
		filepath.Join(dataRoot, "dbus-1", "services", s.ApplicationID+".service"): service}
	var files []File
	for path, data := range raw {
		before, e := Fingerprint(path)
		if e != nil {
			return nil, binding, e
		}
		if before.Exists {
			return nil, binding, fmt.Errorf("immutable callback registration already exists")
		}
		mode := uint32(0600)
		if path == s.Reader {
			mode = 0700
		}
		files = append(files, File{Path: path, Before: before, Data: data, Mode: mode})
	}
	return files, binding, nil
}
