package main

import "os"

func init() {
	// Retain the real CLI entry and writer declaration on every managed-image
	// platform, rather than relying on unrelated test bodies to link them.
	if len(os.Args) > 1 && os.Args[1] == "internal-writer-protocol" {
		main()
		os.Exit(0)
	}
}
