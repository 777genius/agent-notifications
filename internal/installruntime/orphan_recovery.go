package installruntime

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

const transactionSchemaV5 = 5
const maxOrphanPaths = 4096

// OrphanRecoveryRequest fences a single observed consumer, never a guessed
// namespace. Registration and Commands must be the complete recorded values.
type OrphanRecoveryRequest struct {
	InstallationID     string
	ExpectedGeneration uint64
	ConsumerID         string
	RuntimeRoot        string
	Consumer           Consumer
}

// OrphanRecoveryPreview is an observation, not a lease. On conflict the bounded
// result and error both describe the failed admission; commit repeats admission.
type OrphanRecoveryPreview struct {
	InstallationID        string
	Generation            uint64
	ConsumerID            string
	RuntimeRoot           string
	SelectedPaths         []string
	SelectedCount         int
	Admissible            bool
	ConflictCode          string
	ConflictPath          string
	NativeValidated       bool
	NativeIdentityRefresh bool
	ExpectedPolicy        Identity
}

// OrphanRecoveryConflict permits an adapter to expose bounded categories.
type OrphanRecoveryConflict struct {
	Code, Path string
	Err        error
}

func (e *OrphanRecoveryConflict) Error() string {
	return fmt.Sprintf("orphan recovery %s at %q: %v", e.Code, e.Path, e.Err)
}
func (e *OrphanRecoveryConflict) Unwrap() error { return e.Err }
func orphanConflict(code, path string, err error) error {
	return &OrphanRecoveryConflict{Code: code, Path: path, Err: err}
}

type orphanAbsence struct {
	Path    string
	Missing string
	Parents []PathAnchor
}

// The full before-image and identities are in transaction.Before. Registration
// is a separate observation: it never expands Files or the publication set.
type orphanDecision struct {
	ControlRoot            string
	ControlParents         []PathAnchor
	ControlLocks           []PathAnchor
	Request                OrphanRecoveryRequest
	Files                  []string
	Identities             []Identity
	SourcePolicyGeneration uint64
	RuntimeAbsences        []orphanAbsence
	RegistrationAbsence    *orphanAbsence
	Policy                 Identity
}

func cleanPhysicalPath(path string) bool {
	if path == "" || len(path) > 4096 || !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) == path || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	physical, err := PhysicalPath(path)
	return err == nil && physical == path
}

func orphanOverlap(a, b string) bool {
	if runtime.GOOS == "windows" {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return a == b || pathWithinRoot(a, b) || pathWithinRoot(b, a)
}

func validateOrphanRequest(req OrphanRecoveryRequest) error {
	if req.InstallationID == "" || len(req.InstallationID) > 256 || req.ExpectedGeneration == 0 || req.ConsumerID == "" || len(req.ConsumerID) > 4096 || !cleanPhysicalPath(req.RuntimeRoot) || req.Consumer.RuntimeRoot != req.RuntimeRoot {
		return orphanConflict("selection", req.RuntimeRoot, fmt.Errorf("exact installation, generation, consumer and physical runtime required"))
	}
	return nil
}
