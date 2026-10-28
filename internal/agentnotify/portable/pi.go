package portable

import (
	"bytes"
	"path/filepath"
	"strings"
)

// ValidatePiExecutable bounds the initial Windows distribution to a native
// standalone executable. npm's pi.cmd requires a separately tested launcher.
func ValidatePiExecutable(path, goos string) error {
	if goos == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") {
		return ErrInvalid
	}
	return nil
}

// ReadPiBinding selects canonical private locator bytes only. Live installed
// receipts and policy must still qualify the binding before every effect.
func ReadPiBinding(selector string) (Binding, error) {
	if !filepath.IsAbs(selector) || filepath.Clean(selector) != selector {
		return Binding{}, ErrInvalid
	}
	data, name := filepath.Dir(selector), filepath.Base(selector)
	raw, err := readPrivate(data, name)
	if err != nil {
		return Binding{}, ErrInvalid
	}
	b, err := decode(raw)
	if err != nil || b.Integration != Pi || b.DataRoot != data {
		return Binding{}, ErrInvalid
	}
	wanted, err := b.Filename()
	if err != nil || wanted != name {
		return Binding{}, ErrInvalid
	}
	_, _, canonical, err := b.Registration()
	if err != nil || !bytes.Equal(raw, canonical) {
		return Binding{}, ErrInvalid
	}
	return b, nil
}
