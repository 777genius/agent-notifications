//go:build !linux && !darwin

package portable

func physicalDirectory(string) error             { return ErrInvalid }
func readPrivate(string, string) ([]byte, error) { return nil, ErrInvalid }
func checkPrimary(string) error                  { return ErrInvalid }
func writePrivate(string, string, []byte) error  { return ErrInvalid }
func removePrivate(string, string) error         { return ErrInvalid }
