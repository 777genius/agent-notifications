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
	causeStage string
	causeErrno uintptr
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
