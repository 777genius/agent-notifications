//go:build !darwin && !linux

package notifier

func contextNativeDiagnosticSink() func(contextNativeDiagnostic) { return nil }
func contextNativeDiagnosticSignal(error) (*bool, *int)          { return nil, nil }
