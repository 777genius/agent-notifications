//go:build darwin && !cgo

package notifier

import "errors"

// Cross-builds without Darwin's kernel process APIs cannot own a launched app.
// Keep those builds valid while denying the opt-in context delivery path.
func currentContextNativeProcess() (contextNativeProcess, error) {
	return contextNativeProcess{}, errors.New("native_process_identity_requires_cgo")
}

func stopContextNativeProcess(contextNativeProcess, string) error {
	return errors.New("native_process_identity_requires_cgo")
}
