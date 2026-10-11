package main

import (
	"context"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
)

// exportJob is the interval export: the Markdown export of the database in
// c.DataDir into c.ExportDir every c.ExportInterval, starting at once. A run
// exports nothing while the newest committed transaction is the existing
// export's last_transaction. ok is false when the interval is zero, the
// default.
//
// Each run reads its own read-only snapshot, like `lit-server export`,
// rather than one of the server's read snapshots, so an export of a large
// tracker never holds a read slot that requests wait for, and an error it
// meets never triggers the server's database-failure exit.
func exportJob(c service.Config, logf func(format string, args ...any)) (job periodic, ok bool) {
	every := time.Duration(c.ExportInterval)
	if every <= 0 {
		return periodic{}, false
	}
	logf("lit-server: interval exports every %s into %s", every, c.ExportDir)
	return periodic{name: "interval export", every: every, run: func(ctx context.Context) error {
		m, exported, e := exportDatabase(ctx, c.DataDir, c.ExportDir, true)
		if e != nil {
			return e
		}
		if exported {
			n := m.Counts
			logf("lit-server: exported %d projects, %d issues, %d comments and %d milestones to %s", n.Projects, n.Issues, n.Comments, n.Milestones, c.ExportDir)
		}
		return nil
	}}, true
}
