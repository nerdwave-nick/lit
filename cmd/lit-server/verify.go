package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/nwcli"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// verifyCommand is `lit-server verify`, the on-demand full verification.
var verifyCommand = &nwcli.Command{
	Name:    "lit-server verify",
	Summary: "Verify the database of a stopped lit-server",
	Description: `Verify the database of a stopped lit-server, on demand; it never runs at startup.

It runs the full integrity scan that lit-server runs at a start after an
unclean shutdown: the FTS5 integrity-check of every search index (a failed
index is rebuilt, as at startup, and reported on stderr), PRAGMA
integrity_check and PRAGMA foreign_key_check. Then it replays the field and body
history of every record from its creation, in one read snapshot, and compares
the result with the record's current state. The computed membership lists
(a project's issue_ids, an issue's child_ids and comment_ids) are skipped.
Each problem is printed as one line on stdout. A database that verified is left
with the clean-shutdown marker (lit.db.clean), so the next start skips the
scan; any other outcome leaves none, so the next start runs it.

lit-server verify takes the data directory's owner lock, so lit-server must be
stopped. It never creates or migrates a database. Run it after restoring a
backup snapshot, before starting lit-server on it, and after a repair.
The data directory comes from the same flags, environment and JSON config as
lit-server.

Exit status: 0 when the database verified, 1 when a check or the replay found a
problem or the database could not be opened, 2 for invalid arguments.`,
	Example: "  lit-server verify\n  lit-server verify --data-dir /var/lib/lit-server",
	Flags: []nwcli.Flag{
		{Name: "config", Usage: "Optional JSON config `FILE` (or LIT_CONFIG_FILE)", Value: nwcli.Value{Kind: nwcli.Path}},
		{Name: "data-dir", Usage: "Optional data `DIRECTORY` to verify (or LIT_DATA_DIR)", Value: nwcli.Value{Kind: nwcli.Dir}},
	},
}

// runVerify runs `lit-server verify` with the arguments after "verify".
func runVerify(args []string, out, errOut io.Writer) int {
	p, err := verifyCommand.Parse(args)
	switch {
	case err != nil:
		fmt.Fprintln(errOut, err)
		return 2
	case p.Help:
		verifyCommand.WriteHelp(out)
		return 0
	}
	normalized := []string{}
	for _, name := range []string{"config", "data-dir"} {
		for _, v := range p.Flags[name] {
			normalized = append(normalized, "--"+name, v)
		}
	}
	c, err := service.LoadConfig(normalized)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	db, err := storage.OpenExisting(c.DataDir)
	if err != nil {
		hint := ""
		if strings.Contains(err.Error(), "already owned") {
			hint = "; stop lit-server before running lit-server verify"
		}
		fmt.Fprintf(errOut, "lit-server verify: %v%s\n", err, hint)
		return 1
	}
	defer db.Close()
	logf := func(format string, args ...any) { fmt.Fprintf(errOut, format+"\n", args...) }
	r, err := db.Verify(context.Background(), logf)
	if err != nil {
		fmt.Fprintln(errOut, "lit-server verify:", err)
		return 1
	}
	for _, problem := range r.Problems {
		fmt.Fprintln(out, problem)
	}
	if n := len(r.Problems); n > 0 {
		plural := "s"
		if n == 1 {
			plural = ""
		}
		fmt.Fprintf(out, "lit-server verify: %d problem%s found in %s (%d records, %d revisions replayed)\n", n, plural, c.DataDir, r.Records, r.Revisions)
		return 1
	}
	fmt.Fprintf(out, "lit-server verify: %s verified: startup checks passed; %d records match their replayed history (%d revisions)\n", c.DataDir, r.Records, r.Revisions)
	// The database verified, so the next start may skip the full scan.
	if err = db.CloseClean(); err != nil {
		fmt.Fprintf(errOut, "lit-server verify: %v; the next start runs the full integrity scan\n", err)
	}
	return 0
}
