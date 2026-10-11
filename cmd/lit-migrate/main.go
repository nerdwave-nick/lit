// lit-migrate is the one-time setup import of a stopped legacy lit file store
// into a new, empty lit-server data directory. It contains a frozen copy of
// the legacy reader, which lit-server never links, and is deleted in a later
// release.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/importer"
	"github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacy"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/nwcli"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
)

var command = &nwcli.Command{
	Name:    "lit-migrate",
	Summary: "Import a stopped legacy lit file store into a new lit-server database",
	Description: `Import a legacy lit file store, once, into a new SQLite database for lit-server.
Run it during the setup step described in docs/installation.md, after the old
lit-server is stopped and before the new lit-server first starts.

--from names a copy of the stopped legacy data directory; lit-migrate refuses a
store that a running lit-server still owns. It never writes to --from: it copies
it into a private staging directory and reads only that copy with the frozen
legacy reader, which verifies it as the legacy lit-server did at startup.
--to names the new data directory; it must be missing or empty. The database is
written to a temporary file in one transaction, checked (legacy verification,
complete file consumption, counts, record, body, diff, history and stamp
equality, then lit-server verify), and moved into --to only after every check
passed. A failure leaves no database, so the import can be run again. The
service ID carries over unchanged, and the body SHA-256 manifest is kept in
--to/backup for audits.

--config names the legacy lit-server's JSON config, whose title prefixes legacy
verification checks; it defaults to the lit-server config file
(LIT_CONFIG_FILE or ${XDG_CONFIG_HOME:-$HOME/.config}/lit-server/config.json).
--dry-run runs every step and check in a temporary directory, never touches
--to, and reports how long the import takes.

Exit status: 0 when the import (or dry run) passed every check, 1 when it
failed, 2 for invalid arguments.`,
	Example: "  lit-migrate --from /scratch/lit-server.copy --to ~/.local/share/lit-server.db --dry-run\n" +
		"  lit-migrate --from /scratch/lit-server.copy --to ~/.local/share/lit-server.db --config ~/.config/lit-server/config.json",
	Flags: []nwcli.Flag{
		{Name: "from", Usage: "Required copy of the stopped legacy data `DIRECTORY`", Value: nwcli.Value{Kind: nwcli.Dir}},
		{Name: "to", Usage: "Required new, empty data `DIRECTORY` for lit-server", Value: nwcli.Value{Kind: nwcli.Dir}},
		{Name: "config", Usage: "Optional legacy lit-server JSON config `FILE` (or LIT_CONFIG_FILE)", Value: nwcli.Value{Kind: nwcli.Path}},
		{Name: "dry-run", Switch: true, Usage: "Run and check the import without touching --to"},
		{Name: "version", Short: "v", Switch: true, Usage: "version for lit-migrate"},
	},
}

func main() { os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr)) }

// runCLI parses the command line before reading any configuration or store.
func runCLI(args []string, out, errOut io.Writer) int {
	p, err := command.Parse(args)
	switch {
	case err != nil:
		fmt.Fprintln(errOut, err)
		return 2
	case p.Help:
		command.WriteHelp(out)
		return 0
	case len(p.Flags["version"]) > 0:
		fmt.Fprintln(out, protocol.ReleaseVersion)
		return 0
	}
	one := func(name string) (string, bool) {
		v := p.Flags[name]
		if len(v) != 1 || v[0] == "" {
			fmt.Fprintf(errOut, "lit-migrate needs --%s exactly once; see lit-migrate --help\n", name)
			return "", false
		}
		return v[0], true
	}
	from, ok := one("from")
	if !ok {
		return 2
	}
	to, ok := one("to")
	if !ok {
		return 2
	}
	var configArgs []string
	for _, v := range p.Flags["config"] {
		configArgs = append(configArgs, "--config", v)
	}
	c, err := service.LoadConfig(configArgs)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	o := importer.Options{
		From: from, To: to, DryRun: len(p.Flags["dry-run"]) > 0,
		Config: legacy.Config{SchemaVersion: 1, Limits: c.Limits, TitlePrefixes: c.TitlePrefixes},
		Logf:   func(format string, args ...any) { fmt.Fprintf(errOut, format+"\n", args...) },
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return report(ctx, o, out, errOut)
}

// report runs the import and prints its outcome and duration.
func report(ctx context.Context, o importer.Options, out, errOut io.Writer) int {
	start := time.Now()
	r, err := importer.Run(ctx, o)
	took := time.Since(start).Round(time.Millisecond)
	if err != nil {
		fmt.Fprintf(errOut, "lit-migrate: %v\nlit-migrate: failed after %s; no database was written to %s\n", err, took, o.To)
		return 1
	}
	n := r.Counts
	fmt.Fprintf(out, "lit-migrate: service %s: %d projects, %d issues, %d comments, %d milestones, %d transactions, %d history rows (%d history files), %d clients, %d claims\n",
		r.ServiceID, n.Projects, n.Issues, n.Comments, n.Milestones, n.Transactions, n.History, r.HistoryFiles, n.Clients, n.Claims)
	if o.DryRun {
		fmt.Fprintf(out, "lit-migrate: dry run passed all %d checks in %s; %s was not touched\n", len(r.Checks), took, o.To)
		return 0
	}
	fmt.Fprintf(out, "lit-migrate: imported into %s in %s; all %d checks passed; body manifest %s\n", r.Database, took, len(r.Checks), r.Manifest)
	return 0
}
