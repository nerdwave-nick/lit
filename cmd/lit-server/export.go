package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/export"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/nwcli"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// exportCommand is `lit-server export`, routed by runCLI when "export" is
// the first argument.
var exportCommand = &nwcli.Command{
	Name:    "lit-server export",
	Summary: "Write the tracker as a Markdown tree for people",
	Description: `Write the tracker as a one-way Markdown tree for people to browse: README.md and
manifest.json, one directory per project title with project.md, issues/ and
milestones/, and a .history.md file next to each record.
The tree is never read back; edits are overwritten by the next export. It is not
a backup: claims, clients and transaction evidence are omitted.

The export reads one consistent snapshot of the database in the data directory.
It never takes the owner lock and never writes the database, so it works when
lit-server is stopped and also while lit-server runs, which keeps ownership and
goes on serving requests. The database must exist and have this binary's schema
version.

The tree is written to a staging directory next to DIRECTORY and switched into
place atomically, so readers never see a half-written export. DIRECTORY must be
missing, empty or a previous export; its parent must be writable and holds a lock
file that keeps a second export into the same directory out.`,
	Example: "  lit-server export --to ./lit-export\n  lit-server export --data-dir /var/lib/lit-server --to /srv/lit-export",
	Flags: []nwcli.Flag{
		{Name: "to", Usage: "Required export `DIRECTORY` (missing, empty or a previous export)", Value: nwcli.Value{Kind: nwcli.Dir}},
		{Name: "config", Usage: "Optional JSON config `FILE` (or LIT_CONFIG_FILE)", Value: nwcli.Value{Kind: nwcli.Path}},
		{Name: "data-dir", Usage: "Optional data `DIRECTORY` holding the database (or LIT_DATA_DIR)", Value: nwcli.Value{Kind: nwcli.Dir}},
	},
}

// runExport runs `lit-server export` with the arguments after "export".
func runExport(args []string, out, errOut io.Writer) int {
	p, err := exportCommand.Parse(args)
	switch {
	case err != nil:
		fmt.Fprintln(errOut, err)
		return 2
	case p.Help:
		exportCommand.WriteHelp(out)
		return 0
	case len(p.Flags["to"]) == 0:
		fmt.Fprintln(errOut, "lit-server export requires --to DIRECTORY; run 'lit-server export --help'")
		return 2
	}
	normalized := []string{}
	for _, name := range []string{"config", "data-dir"} {
		for _, v := range p.Flags[name] {
			normalized = append(normalized, "--"+name, v)
		}
	}
	config, err := service.LoadConfig(normalized)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	to := p.Flags["to"][0]
	m, err := exportDatabase(ctx, config.DataDir, to)
	if err != nil {
		fmt.Fprintln(errOut, "lit-server export:", err)
		return 1
	}
	c := m.Counts
	fmt.Fprintf(out, "exported %d projects, %d issues, %d comments and %d milestones to %s\n", c.Projects, c.Issues, c.Comments, c.Milestones, to)
	return 0
}

func exportDatabase(ctx context.Context, dataDir, to string) (export.Manifest, error) {
	db, err := storage.OpenReadOnly(dataDir)
	if err != nil {
		return export.Manifest{}, err
	}
	defer db.Close()
	snap, err := db.Read(ctx)
	if err != nil {
		return export.Manifest{}, err
	}
	defer snap.Close()
	return export.Export(ctx, snap, to, export.Options{ServiceID: db.ServiceID(), Time: snap.Now()})
}
