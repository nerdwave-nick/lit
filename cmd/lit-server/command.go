package main

import (
	"fmt"
	"io"

	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/service"
)

// serverCommand is lit-server's command line: optional flags and no operands.
// The subcommands export, verify and healthcheck have their own command lines
// (see runCLI).
var serverCommand = &nwcli.Command{
	Name:    "lit-server",
	Summary: "Run lit-server in the foreground on a loopback address",
	Description: `Run lit-server in the foreground. All flags are optional; no installation or background
service is created. Stop with Ctrl-C. The data directory is initialized under an
exclusive lock, and an occupied listen address fails without selecting another port.

Configuration precedence: flags > environment > JSON config > defaults.
The config file defaults to ${XDG_CONFIG_HOME:-$HOME/.config}/lit-server/config.json.
LIT_CONFIG_FILE or --config explicitly selects a file that must exist.
Default data directory: ${XDG_DATA_HOME:-$HOME/.local/share}/lit-server.
Default listener: 127.0.0.1:7411. Listen requires an explicit loopback IP address
(127.0.0.1 or ::1) and a port from 0 to 65535; port 0 asks the OS for a free port.
Existing JSON config must have schema_version: 1. Changes require a restart.
Under systemd socket activation, the single inherited loopback socket replaces
the listen address.

lit-server export --to DIRECTORY writes a one-way Markdown export of the tracker;
see lit-server export --help. lit-server verify checks the database of a stopped
lit-server and replays its history; see lit-server verify --help.
lit-server healthcheck checks that a running lit-server answers on its listen
address, for container healthchecks; see lit-server healthcheck --help.`,
	Example: "  lit-server\n  lit-server --data-dir /tmp/lit-poc --listen 127.0.0.1:7411\n  lit-server --config /path/to/config.json",
	Flags: []nwcli.Flag{
		{Name: "config", Usage: "Optional JSON config `FILE` (or LIT_CONFIG_FILE)", Value: nwcli.Value{Kind: nwcli.Path}},
		{Name: "data-dir", Usage: "Optional authoritative data `DIRECTORY` (or LIT_DATA_DIR)", Value: nwcli.Value{Kind: nwcli.Dir}},
		{Name: "listen", Usage: "Optional loopback `IP:PORT` (or LIT_LISTEN; default 127.0.0.1:7411)"},
		{Name: "version", Short: "v", Switch: true, Usage: "version for lit-server"},
	},
}

// runCLI parses the command line before any configuration is read, so help,
// version and argument errors never touch the data directory.
func runCLI(args []string, out, errOut io.Writer) int {
	switch {
	case len(args) > 0 && args[0] == "export":
		return runExport(args[1:], out, errOut)
	case len(args) == 2 && args[0] == "help" && args[1] == "export":
		return runExport([]string{"--help"}, out, errOut)
	case len(args) > 0 && args[0] == "verify":
		return runVerify(args[1:], out, errOut)
	case len(args) == 2 && args[0] == "help" && args[1] == "verify":
		return runVerify([]string{"--help"}, out, errOut)
	case len(args) > 0 && args[0] == "healthcheck":
		return runHealthcheck(args[1:], out, errOut)
	case len(args) == 2 && args[0] == "help" && args[1] == "healthcheck":
		return runHealthcheck([]string{"--help"}, out, errOut)
	}
	p, err := serverCommand.Parse(args)
	switch {
	case err != nil:
		fmt.Fprintln(errOut, err)
		return 2
	case p.Help:
		serverCommand.WriteHelp(out)
		return 0
	case len(p.Flags["version"]) > 0:
		fmt.Fprintln(out, protocol.ReleaseVersion)
		return 0
	}
	normalized := []string{}
	for _, name := range []string{"config", "data-dir", "listen"} {
		for _, v := range p.Flags[name] {
			normalized = append(normalized, "--"+name, v)
		}
	}
	config, err := service.LoadConfig(normalized)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	return runService(config, errOut)
}
