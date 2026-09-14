// Command build-portable-package writes the version-bound Agent Notify portable
// zip for one GOOS/GOARCH using the already-built release executable.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/config"
)

func main() {
	version := flag.String("version", config.ConsumerVersion, "release version without v prefix")
	goos := flag.String("os", "", "GOOS")
	goarch := flag.String("arch", "", "GOARCH")
	executable := flag.String("executable", "", "absolute path to the final platform binary")
	output := flag.String("output", "", "absolute zip path")
	workdir := flag.String("workdir", "", "absolute directory for the expanded package")
	flag.Parse()
	if *goos == "" || *goarch == "" || *executable == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "usage: build-portable-package -os GOOS -arch GOARCH -executable PATH -output ZIP")
		os.Exit(2)
	}
	execPath, err := filepath.Abs(*executable)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	outPath, err := filepath.Abs(*output)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	root := *workdir
	if root == "" {
		root = filepath.Join(filepath.Dir(outPath), "portable-"+*goos+"-"+*goarch)
	} else {
		root, err = filepath.Abs(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	got, err := portableasset.Build(portableasset.BuildRequest{
		Version: *version, GOOS: *goos, GOARCH: *goarch,
		Executable: execPath, OutputRoot: root, Archive: outPath,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s %s\n", got.Archive, got.ArchiveSHA256)
}
