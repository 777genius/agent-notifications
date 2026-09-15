package uapinstaller

import "errors"

var (
	ErrInvalidConfig          = errors.New("installer config rejected")
	ErrUnsupported            = errors.New("installer operation is not published in this beta")
	ErrInvalidHandle          = errors.New("prepared operation does not belong to this engine")
	ErrHandleClosed           = errors.New("prepared operation is closed")
	ErrHandleBusy             = errors.New("prepared operation is applying")
	ErrAlreadyApplied         = errors.New("prepared operation already reached a terminal apply")
	ErrCancelled              = errors.New("installer apply cancelled")
	ErrRecoveryRequired       = errors.New("installer recovery required")
	ErrPlanChanged            = errors.New("installer recovery plan changed")
	ErrInvalidRequest         = errors.New("installer request rejected")
	ErrAmbiguousInstallations = errors.New("ambiguous installations")
	ErrIncomplete             = errors.New("required components missing from plan")
	ErrUpdateRequired         = errors.New("install cannot change an active revision; use update")
	ErrNotInstalled           = errors.New("update and repair require an existing owned binding")
	ErrAssessmentRejected     = errors.New("package assessment is not allow")
)
