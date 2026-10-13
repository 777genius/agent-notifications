package opencodeevent

import "io"

// WriteClockSnapshot serves only the exact private command protocol.
func WriteClockSnapshot(args []string, output io.Writer, port SnapshotPort) int {
	// Strict private protocol: no flag parser, help, paths or raw errors.
	const unavailable = "{\"protocol\":1,\"error\":\"clock_unavailable\"}\n"
	if len(args) != 2 || args[0] != "--protocol" || args[1] != "1" || port == nil {
		_, _ = io.WriteString(output, unavailable)
		return 1
	}
	s, err := port.SampleSnapshot()
	if err == nil {
		var raw []byte
		raw, err = s.JSON()
		if err == nil {
			if n, writeErr := output.Write(raw); writeErr == nil && n == len(raw) {
				return 0
			}
			return 1
		}
	}
	_, _ = io.WriteString(output, unavailable)
	return 1
}
