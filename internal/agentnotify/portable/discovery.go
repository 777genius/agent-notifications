package portable

import (
	"context"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Discovery selects the endpoint epoch for an already registered binding.
// It is metadata only: it never grants permission to execute Executable. A
// launcher must acquire the full integrity lease before starting that file.
type Discovery struct {
	Binding    Binding
	Executable string
	SHA256     string
}

// CheckRuntimeSnapshot binds policy observation to the daemon's executable
// epoch. Checking a separate earlier discovery would permit a refresh between
// that check and the snapshot which later authorizes native delivery.
func (b Binding) CheckRuntimeSnapshot(snapshot installruntime.InstalledSnapshot, sha string) error {
	if err := b.CheckSnapshot(snapshot); err != nil {
		return err
	}
	_, identity, err := b.ownedPrimary(snapshot.Ledger)
	if err != nil || identity.SHA256 != sha {
		return ErrInvalid
	}
	return nil
}

// Discover avoids hashing every installed asset in every lightweight MCP
// bridge. The shared daemon still checks policy/integrity for requests, and
// elected startup must use Acquire before executing the installed primary.
func Discover(ctx context.Context, data, name string) (Discovery, error) {
	b, err := readLaunchBinding(ctx, data, name)
	if err != nil {
		return Discovery{}, err
	}
	l, recovery, err := installruntime.ReadOwnership(b.ControlRoot)
	if err != nil || b.CheckSnapshot(installruntime.InstalledSnapshot{Ledger: l, Recovery: recovery}) != nil {
		return Discovery{}, ErrInvalid
	}
	executable, identity, err := b.ownedPrimary(l)
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{Binding: b, Executable: executable, SHA256: identity.SHA256}, nil
}

func readLaunchBinding(ctx context.Context, data, name string) (Binding, error) {
	if ctx == nil || !selector.MatchString(name) {
		return Binding{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Binding{}, err
	}
	raw, err := readPrivate(data, name)
	if err != nil {
		return Binding{}, ErrInvalid
	}
	b, err := decode(raw)
	if err != nil || !samePhysicalPath(b.DataRoot, data) {
		return Binding{}, ErrInvalid
	}
	filename, _ := b.Filename()
	if filename != name {
		return Binding{}, ErrInvalid
	}
	for _, path := range []string{b.ScopeRoot, b.ControlRoot, b.RuntimeRoot, filepath.Dir(b.GlobalConfig)} {
		if physicalDirectory(path) != nil {
			return Binding{}, ErrInvalid
		}
	}
	return b, nil
}
