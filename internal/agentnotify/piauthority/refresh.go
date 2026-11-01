package piauthority

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// RefreshInstalled republishes Pi's derived runtime locator after a sibling
// installs new shared executable bytes. It never manufactures a consumer,
// changes consent, or trusts a locator as authority; Publish revalidates the
// actual installed UAP package and ownership receipt before writing.
func RefreshInstalled(ctx context.Context, controlRoot string) error {
	if controlRoot == "" {
		var err error
		controlRoot, err = installruntime.ControlRoot()
		if err != nil {
			return err
		}
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(controlRoot)
	if err != nil {
		return err
	}
	if snapshot.Recovery || snapshot.Ledger.PendingMutation != nil {
		return installruntime.ErrPolicyRecovery
	}
	var bindings []portable.Binding
	var failures []error
	for key, consumer := range snapshot.Ledger.Consumers {
		if !strings.HasPrefix(key, "portable:") {
			continue
		}
		var b portable.Binding
		if err := json.Unmarshal([]byte(consumer.Registration), &b); err != nil {
			failures = append(failures, fmt.Errorf("installed portable consumer %s: unreadable registration: %w", key, ErrDenied))
			continue
		}
		if b.Integration != portable.Pi {
			continue
		}
		wantKey, wantConsumer, _, err := b.Registration()
		if err != nil || wantKey != key || !reflect.DeepEqual(consumer, wantConsumer) ||
			b.ControlRoot != controlRoot || b.CheckSnapshot(snapshot) != nil {
			failures = append(failures, fmt.Errorf("installed Pi consumer %s: registration or snapshot validation failed: %w", key, ErrDenied))
			continue
		}
		bindings = append(bindings, b)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].BindingID < bindings[j].BindingID })
	// One invalid Pi installation must not strand other healthy profiles after
	// the shared executable has already been committed by a sibling product.
	for _, b := range bindings {
		if err := Publish(ctx, b, stateFile(b)); err != nil {
			failures = append(failures, fmt.Errorf("refresh installed Pi binding %s: %w", b.BindingID, err))
		}
	}
	return errors.Join(failures...)
}
