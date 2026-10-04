package opencodeinstall

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
	"sync"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// BundleRenderer is the setup-only E2 port for the third fixed origin token.
// A nil renderer keeps legacy placement usable, without admitting its frames.
type BundleRenderer interface {
	RenderRegistration(executable, controlRoot, origin string) ([]byte, error)
}

func preparedRegistration(previous *installruntime.OpenCodeRegistration) (installruntime.OpenCodeRegistration, error) {
	if previous != nil {
		if !previous.Valid() {
			return installruntime.OpenCodeRegistration{}, errors.New("corrupt OpenCode registration")
		}
		return *previous, nil
	}
	r := installruntime.OpenCodeRegistration{}
	for _, field := range []*string{&r.Origin, &r.Salt, &r.Namespace} {
		var bits [32]byte
		if _, err := rand.Read(bits[:]); err != nil {
			return r, err
		}
		*field = hex.EncodeToString(bits[:])
	}
	return r, nil
}

// RegistrationLease can only be constructed by exact kernel lease validation.
// Native references retain the same fence; there is no reacquisition or bypass.
type RegistrationLease struct {
	ctx                context.Context
	mu                 sync.Mutex
	snapshot           installruntime.PolicySnapshot
	registration       installruntime.OpenCodeRegistration
	desktop, webhook   bool
	refs               int
	closed, nativeUsed bool //nolint:unused // nativeUsed fences the Darwin retained native delivery.
	release            func()
}

func AcquireRegistration(ctx context.Context, root string, expected installruntime.PolicySnapshot, executable, goos, goarch, origin string) (*RegistrationLease, error) {
	s, release, err := installruntime.AcquirePolicyLease(ctx, root, expected)
	if err != nil {
		return nil, err
	}
	if s.Policy != expected.Policy || !reflect.DeepEqual(s.Fields, expected.Fields) {
		release()
		return nil, errors.New("OpenCode configuration snapshot changed")
	}
	desktop, webhook := ChannelsFromSnapshot(s, executable, goos, goarch)
	r := s.Installation.Ledger.Consumers[consumerID].OpenCode
	if (!desktop && !webhook) || s.Installation.Ledger.PendingMutation != nil || s.Installation.Ledger.Schema != 4 || s.Installation.Ledger.WriterFloor != installruntime.OpenCodeWriterFloor || r == nil || !r.Valid() || !r.OriginBound || r.Origin != origin {
		release()
		return nil, errors.New("OpenCode registration unavailable")
	}
	owned, ok := installruntime.OwnedFile(s.Installation.Ledger, s.Installation.Ledger.Consumers[consumerID].Registration)
	if !ok || owned.SHA256 != r.BundleSHA256 {
		release()
		return nil, errors.New("OpenCode bundle binding unavailable")
	}
	return &RegistrationLease{ctx: ctx, snapshot: s, registration: *r, desktop: desktop, webhook: webhook, refs: 1, release: release}, nil
}

func (l *RegistrationLease) Registration() installruntime.OpenCodeRegistration { return l.registration }
func (l *RegistrationLease) Channels() (bool, bool)                            { return l.desktop, l.webhook }
func (l *RegistrationLease) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		l.dropReference()
	}
}

func (l *RegistrationLease) dropReference() {
	l.refs--
	if l.refs == 0 {
		l.release()
	}
}
