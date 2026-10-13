package config

import (
	"errors"
	"fmt"
	"syscall"
)

// Code is a stable, content-free diagnostic category.
type Code string

const (
	ConfigUnsafeTarget         Code = "ConfigUnsafeTarget"
	ConfigConflict             Code = "ConfigConflict"
	ConfigLockTimeout          Code = "ConfigLockTimeout"
	ConfigCommitUncertain      Code = "ConfigCommitUncertain"
	ConfigLegacyImportRequired Code = "ConfigLegacyImportRequired"
	ConfigOverrideInvalid      Code = "ConfigOverrideInvalid"
	ConfigHomeUnavailable      Code = "ConfigHomeUnavailable"
	ConfigBaseUnavailable      Code = "ConfigBaseUnavailable"
	ConfigInvalid              Code = "ConfigInvalid"
	ConfigUnsupportedSchema    Code = "ConfigUnsupportedSchema"
	ConfigPermissionDenied     Code = "ConfigPermissionDenied"
	ConfigRecoveryRequired     Code = "ConfigRecoveryRequired"
	ConfigMultipleCandidates   Code = "ConfigMultipleCandidates"
	ConfigLinkedPath           Code = "ConfigLinkedPath"
	ConfigPublicReadable       Code = "ConfigPublicReadable"
	ConfigChanged              Code = "ConfigChanged"
	ConfigMissing              Code = "ConfigMissing"
	ConfigEnvConflict          Code = "ConfigEnvConflict"
)

const ConfigOpenCodeWebhookEnvUnsupported Code = "ConfigOpenCodeWebhookEnvUnsupported"

// Error deliberately excludes underlying parser/OS messages and document values.
// Pointer is a JSON pointer; diagnostics intended for external use should omit it
// because unknown field names can themselves contain secrets.
type Error struct {
	Code    Code
	Path    string
	Pointer string `json:"-"`
	Offset  int64
	// causeStage and causeErrno are deliberately unexported, diagnostic-only
	// metadata for package tests. They let native stress tests distinguish an
	// OS race from a document error without exposing raw OS messages or paths.
	causeStage        string
	causeErrno        uintptr
	permissionFailure *PermissionFailure
}

// PermissionFailure identifies only native filesystem security metadata.
// It never includes OS messages, document contents, or resolved account names.
type PermissionFailure struct {
	ObjectPath string `json:"objectPath"`
	Reason     string `json:"reason"`
	TrusteeSID string `json:"trusteeSid,omitempty"`
	OwnerSID   string `json:"ownerSid,omitempty"`
	AccessMask uint32 `json:"accessMask,omitempty"`
}

// PermissionFailure returns a copy so callers cannot mutate error metadata.
func (e *Error) PermissionFailure() *PermissionFailure {
	if e.permissionFailure == nil {
		return nil
	}
	copy := *e.permissionFailure
	return &copy
}

func (e *Error) Error() string { return fmt.Sprintf("%s: path=%q offset=%d", e.Code, e.Path, e.Offset) }

func sanitizedError(code Code, path string, offset int64, stage string, cause error) *Error {
	e := &Error{Code: code, Path: path, Offset: offset, causeStage: stage}
	var errno syscall.Errno
	if errors.As(cause, &errno) {
		e.causeErrno = uintptr(errno)
	}
	return e
}

type Diagnostic struct {
	Code Code   `json:"code"`
	Path string `json:"path,omitempty"`
}
