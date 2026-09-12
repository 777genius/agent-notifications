//go:build !darwin

package installruntime

func platformAnchorPath(path string) (string, error) { return path, nil }
