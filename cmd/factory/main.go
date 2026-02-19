package main

import (
	"fmt"
	"os"

	"github.com/lucasnoah/taintfactory/internal/cli"
)

// Version is set at build time via ldflags.
var Version = "dev"

func main() {
	cli.SetVersion(Version)
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
