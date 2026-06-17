package main

import (
	"os"

	"github.com/falleng0d/git-worktreeinclude/internal/cli"
)

func main() {
	app := cli.New(os.Stdout, os.Stderr)
	os.Exit(app.Run(os.Args[1:]))
}
