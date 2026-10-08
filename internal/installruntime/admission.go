package installruntime

import "errors"

// AdmissionOrigin identifies the internal caller, never a displayed path.
type AdmissionOrigin uint8

const (
	AdmissionComponent AdmissionOrigin = iota
	AdmissionControl
	AdmissionPolicy
	AdmissionExternalConfig
	AdmissionRecoveryConfig
)

// AdmissionError adds call-site attribution without changing the error chain.
type AdmissionError struct {
	Phase  string
	Origin AdmissionOrigin
	Err    error
}

func (e *AdmissionError) Error() string {
	label := e.Phase
	switch e.Phase {
	case "component_lock_admission":
		label = "component-lock admission"
	case "control_directory_admission":
		label = "control-directory admission"
	case "config_lock_admission":
		label = "config-lock admission"
	}
	return label + ": " + e.Err.Error()
}
func (e *AdmissionError) Unwrap() error { return e.Err }
func admission(phase string, origin AdmissionOrigin, err error) error {
	return &AdmissionError{Phase: phase, Origin: origin, Err: err}
}

type admissionObservation struct {
	Classification string
	ACEType        *uint8
	ACEFlags       *uint8
	AccessMask     *uint32
	API            string
	ErrorCode      *uint32
	Err            error
}

func (e *admissionObservation) Error() string { return e.Err.Error() }
func (e *admissionObservation) Unwrap() error { return e.Err }

// AdmissionDiagnostic contains only closed, measured facts, with no inode data.
type AdmissionDiagnostic struct {
	Phase          string  `json:"phase"`
	Role           string  `json:"role"`
	Classification string  `json:"classification"`
	ACEType        *uint8  `json:"ace_type,omitempty"`
	ACEFlags       *uint8  `json:"ace_flags,omitempty"`
	AccessMask     *uint32 `json:"access_mask,omitempty"`
	API            string  `json:"api,omitempty"`
	ErrorCode      *uint32 `json:"error_code,omitempty"`
}

// ProjectAdmission omits unmeasured failures. The caller supplies its role for
// an external config; origins remain attached across canonicalization/sorting.
func ProjectAdmission(err error, externalRole string) *AdmissionDiagnostic {
	var a *AdmissionError
	var o *admissionObservation
	if !errors.As(err, &a) || !errors.As(a.Err, &o) {
		return nil
	}
	switch a.Phase {
	case "component_lock_admission", "control_directory_admission", "config_lock_admission":
	default:
		return nil
	}
	if o.Classification != "foreign_mutation_ace" && o.Classification != "windows_api_error" {
		return nil
	}
	role := ""
	switch a.Origin {
	case AdmissionComponent:
		role = "component"
	case AdmissionControl:
		role = "control"
	case AdmissionPolicy:
		role = "policy"
	case AdmissionExternalConfig:
		if externalRole != "global_config" {
			return nil
		}
		role = externalRole
	default:
		return nil
	}
	return &AdmissionDiagnostic{Phase: a.Phase, Role: role, Classification: o.Classification,
		ACEType: o.ACEType, ACEFlags: o.ACEFlags, AccessMask: o.AccessMask, API: o.API, ErrorCode: o.ErrorCode}
}
