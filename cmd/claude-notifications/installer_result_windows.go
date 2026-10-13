//go:build windows

package main

import "os"

func openInstallerResult(path string) (*os.File, error) { return os.Open(path) }
