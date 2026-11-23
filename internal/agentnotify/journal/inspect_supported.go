//go:build linux || darwin || windows

package journal

import "context"

// InspectReadOnly validates the existing journal using the same canonical
// decoder as Open, but without taking its lock or writing state. Snapshots are
// atomically replaced, so a reader observes a complete old or new version.
// The caller separately verifies setup owner, namespace and lock presence.
// Actual mutations still reopen under the journal lock and policy CAS fence.
func InspectReadOnly(ctx context.Context, o Options) (string, error) {
	if ctx == nil {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s, err := newStore(o)
	if err != nil {
		return "", err
	}
	dir, err := openRoot(s.root)
	if err != nil {
		return "", err
	}
	defer func() { _ = dir.Close() }()
	d, err := s.readInspection(dir)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return d.Namespace, nil
}
