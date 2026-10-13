package service

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

func prepareRecordTest(t *testing.T, s *Server, c protocol.Client, project, op string, items ...protocol.ProjectInput) protocol.Prepared {
	t.Helper()
	p, e := s.PrepareRecords(protocol.PrepareRequest{Operation: op, Project: project, Items: items}, c)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func executeRecordTest(t *testing.T, s *Server, c protocol.Client, p protocol.Prepared) {
	t.Helper()
	code, body := executeProjectTest(t, s, c, p)
	if code != 200 {
		t.Fatal(code, string(body))
	}
}

// readTree reads every file below root, by relative path.
func readTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		out[filepath.ToSlash(rel)], e = os.ReadFile(path)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}

// A data directory that still holds a legacy file store is refused; nothing
// of it is read, converted or changed.
func TestLegacyFileStoreIsRefused(t *testing.T) {
	root := t.TempDir()
	original := readTree(t, "testdata/project-only-v1")
	for rel, b := range original {
		target := filepath.Join(root, rel)
		if e := os.MkdirAll(filepath.Dir(target), 0o700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(target, b, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	if db, e := storage.Open(root); !errors.Is(e, storage.ErrLegacyStore) {
		if db != nil {
			db.Close()
		}
		t.Fatalf("legacy store: %v", e)
	}
	if !same(readTree(t, root), original) {
		t.Fatal("refusing the legacy store changed its files")
	}
}

// Creating several issues or comments is all or nothing: a failure after all
// the writes, here an oversized response, leaves no record, history or
// transaction behind, and the same request then succeeds.
func TestRecordBatchIsAllOrNothing(t *testing.T) {
	for _, kind := range []string{"issue.create", "comment.create"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			s, c := projectTestServer(t, root)
			project, parent, first, second := protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()
			executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/crash")}))
			executeRecordTest(t, s, c, prepareRecordTest(t, s, c, project, "issue.create", protocol.ProjectInput{ID: parent, Title: textPointer("Parent")}))
			items := []protocol.ProjectInput{{ID: first, Title: textPointer("First"), Parent: &parent, Content: textPointer("# First\r\n雪")}, {ID: second, Title: textPointer("Second"), Parent: &parent, Content: textPointer("Second\n")}}
			typ := "issues"
			if kind == "comment.create" {
				items = []protocol.ProjectInput{{ID: first, Issue: parent, Content: textPointer("# First\r\n雪")}, {ID: second, Issue: parent, Content: textPointer("Second\n")}}
				typ = "comments"
			}
			plan := prepareRecordTest(t, s, c, project, kind, items...)
			s.Config.Limits.SnapshotBytes = 64
			if code, body := executeProjectTest(t, s, c, plan); code != 413 {
				t.Fatalf("oversized response: %d %s", code, body)
			}
			s.Config.Limits = protocol.DefaultLimits()
			check := func(s *Server, committed bool) {
				t.Helper()
				for _, id := range []string{first, second} {
					_, e := readOne(s, typ, id)
					hs, _ := s.RecordHistory(typ, id)
					if (e == nil) != committed || len(hs) != map[bool]int{false: 0, true: 1}[committed] {
						t.Fatalf("%s %s committed=%v: %v, %d history entries", typ, id, committed, e, len(hs))
					}
				}
				owner, _ := s.Issue(parent)
				p, _ := s.Project(project)
				if owner.Revision != 1 || p.Revision != 1 {
					t.Fatalf("owners revised: issue %d, project %d", owner.Revision, p.Revision)
				}
			}
			check(s, false)
			config := s.Config
			s.DB.Close()
			s = openTestServer(t, root, config)
			check(s, false)
			executeRecordTest(t, s, c, plan)
			check(s, true)
		})
	}
}

// readOne reads one record of any type in its own request.
func readOne(s *Server, typ, id string) (any, error) {
	return inSnapshot(s, func(rs *Server) (any, error) { return rs.readRecord(typ, id) })
}
