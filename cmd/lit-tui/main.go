// Command lit-tui is the terminal UI for people using lit.
package main

import (
	"os"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui"
)

func main() { os.Exit(tui.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
