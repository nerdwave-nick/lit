package main

import (
	"github.com/nerdwave-nick/lit/internal/cli"
	"os"
)

func main() { os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr)) }
