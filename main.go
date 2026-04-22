package main

import (
	"context"
	"os"

	"charm.land/fang/v2"
)

// version and commit are injected at build time via -ldflags.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	root := newRootCmd()

	if err := fang.Execute(
		context.Background(),
		root,
		fang.WithVersion(version),
		fang.WithCommit(commit),
		fang.WithNotifySignal(os.Interrupt),
	); err != nil {
		os.Exit(1)
	}
}
