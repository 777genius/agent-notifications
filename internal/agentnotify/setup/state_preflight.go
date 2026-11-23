package setup

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// inspectInitializedForEnable mirrors provision's non-mutating admission rules.
// The ordinary setup preflight used to validate only route/config/runtime and
// announce "ready" for a missing journal or a policy enabled without setupState.
// Neither an inspection nor a failed setup may adopt/reset private state (#585).
func inspectInitializedForEnable(o Options, s installruntime.PolicySnapshot) error {
	if err := installruntime.CheckPrivateControlRoot(o.ControlRoot); err != nil {
		return fail("managed_runtime_required", err)
	}
	raw, present := s.Fields["setupState"]
	if !present {
		if s.Policy.Enabled || s.Installation.Ledger.Enabled {
			return fail("initialization_recovery_required",
				fmt.Errorf("enabled installation has no initialization ownership evidence; preserve policy and state"))
		}
		// A pristine disabled install has no journal. A foreign or interrupted
		// staging directory must be inspected rather than adopted as fresh.
		for _, name := range []string{"state", stageName} {
			_, err := os.Lstat(filepath.Join(o.ControlRoot, name))
			if err == nil {
				return fail("initialization_recovery_required",
					fmt.Errorf("unowned %s exists; preserve it for recovery", name))
			}
			if !os.IsNotExist(err) {
				return fail("initialization_recovery_required",
					fmt.Errorf("cannot verify absence of private setup state: %w", err))
			}
		}
		return nil
	}

	var owner ownership
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	parseErr := decoder.Decode(&owner)
	if parseErr == nil {
		parseErr = decoder.Decode(&struct{}{})
		if parseErr == io.EOF {
			parseErr = nil
		} else if parseErr == nil {
			parseErr = fmt.Errorf("unexpected extra ownership data")
		}
	}
	if parseErr != nil || owner.Schema != 1 ||
		owner.InstallationID != s.Installation.Ledger.ID ||
		len(owner.Token) != 64 || owner.DirectoryID == "" ||
		(owner.Phase != "started" && owner.Phase != "ready") ||
		(owner.Phase == "ready" && len(owner.Namespace) != 64) {
		return fail("initialization_recovery_required",
			fmt.Errorf("invalid durable setupState ownership marker; preserve policy and state"))
	}
	if _, err := hex.DecodeString(owner.Token); err != nil {
		return fail("initialization_recovery_required", fmt.Errorf("invalid ownership token: %w", err))
	}

	if owner.Phase != "ready" {
		// An interrupted journal might be resumable. Only Apply may finish
		// it under setup lock and policy generation fencing; this read-only
		// preflight never changes owner markers or initializes journals.
		return fail("initialization_interrupted",
			fmt.Errorf("setup ownership is not ready; inspect the interrupted setup before enabling"))
	}
	return checkProvisioned(o, s)
}
