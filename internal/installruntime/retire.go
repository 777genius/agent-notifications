package installruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
)

// nativeDrainCheck must prove absence of executable mappings and open resources,
// not merely absence of a process with a particular name. It runs while the
// component lock excludes new owned launches. Published generation paths are
// not drained; qualifyRetirement applies only to leftover private .candidate-*
// identities. Candidate paths are never callback routes.
var nativeDrainCheck = verifyNativeDrain

func qualifyRetirement(ctx context.Context, change *NativeChange) error {
	if change == nil || !change.Retire {
		return nil
	}
	before, after := change.Before, change.After
	if change.Purge || before.PreviousPath == "" || after.PreviousPath != "" || after.PreviousSHA256 != "" || after.Path != before.Path || after.SHA256 != before.SHA256 || after.DecoderFloor < 1 || after.DecoderFloor != before.DecoderFloor {
		return fmt.Errorf("retirement must preserve the active published generation")
	}
	expected := before
	expected.PreviousPath, expected.PreviousSHA256, expected.PreviousDirectoryID = "", "", ""
	if !reflect.DeepEqual(expected, after) {
		return fmt.Errorf("retirement changed the active published generation")
	}
	if filepath.Dir(before.PreviousPath) != filepath.Dir(before.Path) || !strings.HasPrefix(filepath.Base(before.PreviousPath), ".candidate-") || change.Staged != before.PreviousPath {
		return fmt.Errorf("retirement predecessor is not a private candidate identity")
	}
	if err := validateNativeRecord(&after); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// After a proven deletion and crash, missing is valid. No adapter may launch
	// a candidate; queued OS callbacks continue through the unchanged live reader.
	if _, err := os.Lstat(before.PreviousPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return nativeDrainCheck(ctx, before.PreviousPath)
}

func drainCommandResult(contextErr, err error, out, diagnostic []byte) error {
	if contextErr != nil {
		return contextErr
	}
	if len(diagnostic) != 0 {
		return fmt.Errorf("native resource scan incomplete; retirement refused")
	}
	if len(out) != 0 {
		return fmt.Errorf("previous native artifact is still in use")
	}
	var status *exec.ExitError
	if errors.As(err, &status) && status.ExitCode() == 1 {
		return nil
	} // lsof: no selected files
	if err != nil {
		return fmt.Errorf("native resource scan failed: %w", err)
	}
	return fmt.Errorf("native resource scan returned ambiguous empty success")
}
