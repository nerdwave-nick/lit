package importer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/cmd/lit-migrate/internal/legacy"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

func run(t *testing.T, from, to string, dryRun bool) (Report, error) {
	t.Helper()
	return Run(context.Background(), Options{From: from, To: to, Config: testConfig(), DryRun: dryRun, TempDir: t.TempDir(), Logf: t.Logf})
}

func mustRun(t *testing.T, from, to string) Report {
	t.Helper()
	r, e := run(t, from, to, false)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func openNew(t *testing.T, dir string) *storage.DB {
	t.Helper()
	db, e := storage.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func readNew(t *testing.T, db *storage.DB, f func(ctx context.Context, q *storage.ReadTx)) {
	t.Helper()
	ctx := context.Background()
	q, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	f(ctx, q)
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, e := os.ReadDir(dir)
	if e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	names := []string{}
	for _, v := range list {
		names = append(names, v.Name())
	}
	return names
}

func TestImportConvertsALegacyStore(t *testing.T) {
	f := newFixture(t)
	from := f.copyOf(t)
	source := tree(t, from)
	to := filepath.Join(t.TempDir(), "new")
	r := mustRun(t, from, to)

	if tree(t, from) != source {
		t.Fatal("the import changed its source directory")
	}
	if len(r.Checks) != 9 || r.ServiceID != f.s.ServiceID() || r.Database != filepath.Join(to, storage.DatabaseFile) {
		t.Fatalf("report %+v", r)
	}
	if got := strings.Join(entries(t, to), " "); got != "backup lit.db" {
		t.Fatalf("target holds %s", got)
	}
	if b, e := os.ReadFile(r.Manifest); e != nil || !strings.Contains(string(b), f.a) || !strings.Contains(string(b), f.s.ServiceID()) || filepath.Dir(r.Manifest) != filepath.Join(to, storage.BackupDir) {
		t.Fatalf("manifest %s: %v", r.Manifest, e)
	}
	want := storage.Counts{Projects: 2, Issues: 5, Comments: 1, Milestones: 1, Transactions: 10, History: r.Counts.History, Clients: 3, Claims: 2}
	if r.Counts != want || r.HistoryFiles != r.Counts.History {
		t.Fatalf("counts %+v (history files %d), want %+v", r.Counts, r.HistoryFiles, want)
	}

	db := openNew(t, to)
	if db.ServiceID() != f.s.ServiceID() {
		t.Fatalf("service %s, want %s", db.ServiceID(), f.s.ServiceID())
	}
	old, e := legacy.Open(f.copyOf(t), testConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	readNew(t, db, func(ctx context.Context, q *storage.ReadTx) {
		// Claims: the live and the expired one, byte for byte; released ones are gone.
		for _, issue := range []string{f.a, f.b, f.c, f.d} {
			got, e := q.Claim(ctx, issue)
			if e != nil {
				t.Fatal(e)
			}
			legacyClaim, e := old.StoredClaim(issue)
			if e != nil {
				t.Fatal(e)
			}
			if jsonOf(got) != jsonOf(legacyClaim) {
				t.Fatalf("claim on %s: %s, want %s", issue, jsonOf(got), jsonOf(legacyClaim))
			}
		}
		if c, _ := q.Claim(ctx, f.b); c == nil {
			t.Fatal("the expired claim was dropped")
		}
		carol, ok, e := q.Client(ctx, f.carol.ClientID)
		if e != nil || !ok || jsonOf(carol) != jsonOf(f.carol) {
			t.Fatalf("client %s, want %s (%v)", jsonOf(carol), jsonOf(f.carol), e)
		}
		// Membership lists are computed, and old entries that changed only them stay.
		a, e := q.Issue(ctx, f.a)
		if e != nil || jsonOf(a.ChildIDs) != jsonOf([]string{f.c}) || jsonOf(a.CommentIDs) != jsonOf([]string{f.comment}) {
			t.Fatalf("issue %+v (%v)", a, e)
		}
		hs, e := q.History(ctx, storage.Projects, f.p1, 0, 0)
		if e != nil {
			t.Fatal(e)
		}
		membershipOnly := 0
		for _, h := range hs {
			if _, ok := h.Differences["issue_ids"]; ok && len(h.Differences) == 3 {
				membershipOnly++
			}
		}
		if membershipOnly != 3 {
			t.Fatalf("%d membership-only project revisions, want 3: %s", membershipOnly, jsonOf(hs))
		}
		// The two-issue creation is one transaction record for three owners.
		tx, found, e := q.Transaction(ctx, hs[1].RequestHash)
		if e != nil || !found || len(tx.Results) != 3 {
			t.Fatalf("transaction %+v (%v, %v)", tx, found, e)
		}
		legacyHistory, e := old.RecordHistory("issues", f.a)
		if e != nil {
			t.Fatal(e)
		}
		newHistory, e := q.History(ctx, storage.Issues, f.a, 0, 0)
		if e != nil || jsonOf(newHistory) != jsonOf(legacyHistory) {
			t.Fatalf("history %s\nwant %s (%v)", jsonOf(newHistory), jsonOf(legacyHistory), e)
		}
	})
	report, e := db.Verify(context.Background(), t.Logf)
	if e != nil || len(report.Problems) != 0 {
		t.Fatal(report.Problems, e)
	}
}

func TestDryRunTouchesNeitherSourceNorTarget(t *testing.T) {
	f := newFixture(t)
	from := f.copyOf(t)
	source := tree(t, from)
	for _, name := range []string{"absent", "empty"} {
		t.Run(name, func(t *testing.T) {
			to := filepath.Join(t.TempDir(), "new")
			if name == "empty" {
				if e := os.Mkdir(to, 0o700); e != nil {
					t.Fatal(e)
				}
			}
			target := tree(t, to)
			staging := t.TempDir()
			r, e := Run(context.Background(), Options{From: from, To: to, Config: testConfig(), DryRun: true, TempDir: staging, Logf: t.Logf})
			if e != nil {
				t.Fatal(e)
			}
			if len(r.Checks) != 9 || r.Database != "" || r.Manifest != "" || r.Counts.Issues != 5 {
				t.Fatalf("report %+v", r)
			}
			if tree(t, to) != target || tree(t, from) != source {
				t.Fatal("a dry run changed the source or the target")
			}
			if left := entries(t, staging); len(left) != 0 {
				t.Fatalf("a dry run left %v", left)
			}
		})
	}
}

func TestImportRefusesANonEmptyTarget(t *testing.T) {
	f := newFixture(t)
	to := t.TempDir()
	if e := os.WriteFile(filepath.Join(to, "keep"), []byte("x"), 0o600); e != nil {
		t.Fatal(e)
	}
	target := tree(t, to)
	for _, dry := range []bool{false, true} {
		if _, e := run(t, f.copyOf(t), to, dry); e == nil || !strings.Contains(e.Error(), "not empty") {
			t.Fatalf("dry run %v: %v", dry, e)
		}
	}
	if tree(t, to) != target {
		t.Fatal("the refused import changed the target")
	}
}

func TestImportRefusesAStoreThatIsStillOwned(t *testing.T) {
	f := newFixture(t)
	from := f.copyOf(t)
	owner, e := legacy.Open(from, testConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	to := filepath.Join(t.TempDir(), "new")
	if _, e := run(t, from, to, false); e == nil || !strings.Contains(e.Error(), "stop") {
		t.Fatal(e)
	}
	if _, e := os.Stat(to); !os.IsNotExist(e) {
		t.Fatalf("the refused import created the target: %v", e)
	}
}

func TestFailuresLeaveNoDatabase(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, want string
		damage     func(t *testing.T, dir string)
	}{
		{"an unlisted file", "check 2", func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, "issues", f.a, "notes.txt"), "?")
		}},
		{"a record that differs from its history", "check 1", func(t *testing.T, dir string) {
			p := filepath.Join(dir, "issues", f.b, "content.md")
			b, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			write(t, p, strings.Replace(string(b), "B renamed", "B tampered", 1))
		}},
		// The legacy server wrote claim times with RFC3339Nano, which drops
		// trailing zeros; it read others too. Such a time cannot be copied
		// exactly, so the import refuses it rather than rewriting it.
		{"a claim time that is not canonical", "is not a canonical UTC RFC3339Nano time", func(t *testing.T, dir string) {
			p := filepath.Join(dir, filepath.FromSlash(legacy.ClaimFile(f.a)))
			var v legacy.ClaimEnvelope
			b, e := os.ReadFile(p)
			if e != nil || json.Unmarshal(b, &v) != nil {
				t.Fatal(e)
			}
			v.Claim.AcquiredAt = strings.TrimSuffix(v.Claim.AcquiredAt, "Z") + "0Z"
			b, _ = json.Marshal(v)
			write(t, p, string(b))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from := f.copyOf(t)
			c.damage(t, from)
			source := tree(t, from)
			to := t.TempDir()
			_, e := run(t, from, to, false)
			if e == nil || !strings.Contains(e.Error(), c.want) {
				t.Fatalf("error %v, want %q", e, c.want)
			}
			if left := entries(t, to); len(left) != 0 {
				t.Fatalf("the failed import left %v", left)
			}
			if tree(t, from) != source {
				t.Fatal("the failed import changed its source")
			}
		})
	}
	t.Run("a created target is removed", func(t *testing.T) {
		from := f.copyOf(t)
		write(t, filepath.Join(from, "stray"), "?")
		to := filepath.Join(t.TempDir(), "new")
		if _, e := run(t, from, to, false); e == nil {
			t.Fatal("imported a store with a stray file")
		}
		if _, e := os.Stat(to); !os.IsNotExist(e) {
			t.Fatalf("the failed import left its target: %v", e)
		}
	})
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if e := os.WriteFile(path, []byte(s), 0o600); e != nil {
		t.Fatal(e)
	}
}

func TestRerunAfterAFailureSucceeds(t *testing.T) {
	f := newFixture(t)
	from := f.copyOf(t)
	stray := filepath.Join(from, "issues", f.a, "notes.txt")
	write(t, stray, "?")
	to := t.TempDir()
	if _, e := run(t, from, to, false); e == nil {
		t.Fatal("imported a stray file")
	}
	if e := os.Remove(stray); e != nil {
		t.Fatal(e)
	}
	if r := mustRun(t, from, to); r.ServiceID != f.s.ServiceID() {
		t.Fatalf("report %+v", r)
	}
}

func TestImportIsDeterministic(t *testing.T) {
	f := newFixture(t)
	dumps := []string{}
	for range 2 {
		to := filepath.Join(t.TempDir(), "new")
		mustRun(t, f.copyOf(t), to)
		dumps = append(dumps, logicalDump(t, to))
	}
	if dumps[0] != dumps[1] {
		t.Fatalf("two imports differ:\n%s\n%s", dumps[0], dumps[1])
	}
}

// logicalDump renders everything an imported database holds through the
// storage read API.
func logicalDump(t *testing.T, dir string) string {
	t.Helper()
	db := openNew(t, dir)
	var b strings.Builder
	readNew(t, db, func(ctx context.Context, q *storage.ReadTx) {
		n, e := q.Counts(ctx)
		if e != nil {
			t.Fatal(e)
		}
		b.WriteString(db.ServiceID() + jsonOf(n) + "\n")
		for _, typ := range []string{storage.Projects, storage.Issues, storage.Comments, storage.Milestones} {
			ids, e := q.RecordIDs(ctx, typ)
			if e != nil {
				t.Fatal(e)
			}
			for _, id := range ids {
				r, e := q.Record(ctx, typ, id)
				if e != nil {
					t.Fatal(e)
				}
				hs, e := q.History(ctx, typ, id, 0, 0)
				if e != nil {
					t.Fatal(e)
				}
				claim, e := q.Claim(ctx, id)
				if e != nil {
					t.Fatal(e)
				}
				b.WriteString(jsonOf(r) + jsonOf(hs) + jsonOf(claim) + "\n")
			}
		}
	})
	return b.String()
}

// The fixture's clients are copied, connected and disconnected alike.
func TestClientsAreCopied(t *testing.T) {
	f := newFixture(t)
	to := filepath.Join(t.TempDir(), "new")
	mustRun(t, f.copyOf(t), to)
	readNew(t, openNew(t, to), func(ctx context.Context, q *storage.ReadTx) {
		for _, c := range []protocol.Client{f.alice, f.bob, f.carol} {
			got, ok, e := q.Client(ctx, c.ClientID)
			if e != nil || !ok || jsonOf(got) != jsonOf(c) {
				t.Fatalf("client %s, want %s (%v)", jsonOf(got), jsonOf(c), e)
			}
		}
	})
}

// The manifest sits in the new data directory's default backup directory;
// lit-server's backup retention there removes only its own snapshots.
func TestBackupRetentionKeepsTheManifest(t *testing.T) {
	f := newFixture(t)
	to := filepath.Join(t.TempDir(), "new")
	r := mustRun(t, f.copyOf(t), to)
	db := openNew(t, to)
	backups := filepath.Join(to, storage.BackupDir)
	for range 2 {
		if _, e := db.Backup(context.Background(), backups); e != nil {
			t.Fatal(e)
		}
	}
	removed, e := storage.PruneBackups(backups, 1)
	if e != nil || len(removed) != 1 {
		t.Fatal(removed, e)
	}
	if _, e := os.Stat(r.Manifest); e != nil {
		t.Fatalf("retention removed the manifest: %v", e)
	}
}
