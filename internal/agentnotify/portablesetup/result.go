package portablesetup

import (
	"errors"
	"fmt"
	"strings"

	"github.com/777genius/agent-notifications/install/uapinstaller"
)

func IsUpdateRequired(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUpdateRequired) || errors.Is(err, uapinstaller.ErrUpdateRequired) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "run update separately") ||
		strings.Contains(msg, "use switch to change source") ||
		strings.Contains(msg, "recorded MCP selection is no longer supported; use an explicit update for controlled removal") ||
		strings.Contains(msg, "is sticky to")
}

func wrapUpdateRequired(err error) error {
	if err == nil {
		return nil
	}
	if IsUpdateRequired(err) && !errors.Is(err, ErrUpdateRequired) {
		return fmt.Errorf("%w: %w", ErrUpdateRequired, err)
	}
	return err
}

// ResultError keeps the installer Result when a mutation already happened.
// Mapping that to a bool or an empty Binding would hide a managed commit.
type ResultError struct {
	Result uapinstaller.Result
	Err    error
	// Finalization distinguishes host work after a successful UAP operation
	// from UAP's own incomplete materialization or activation result.
	Finalization bool
}

func (e ResultError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Result.Reason
}

func (e ResultError) Unwrap() error { return e.Err }

func persistResult(result uapinstaller.Result, err error) error {
	if err == nil {
		return nil
	}
	if result.Outcome == "" && result.Client.Materialization == "" && result.Binding.BindingID == "" {
		return err
	}
	return ResultError{Result: result, Err: err}
}

// finalizationResult preserves the exact UAP result, including unchanged
// operations, without describing a later host failure as failed activation.
func finalizationResult(result uapinstaller.Result, err error) error {
	if err == nil {
		return nil
	}
	return ResultError{Result: result, Err: err, Finalization: true}
}
