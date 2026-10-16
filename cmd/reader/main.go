package main

import (
	"os"

	"reader/internal/cli"
)

func main() {
	cli.ServeCommand = runServe
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
