package drafts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

const (
	project = "0b9c7d4e-1f2a-4b3c-8d5e-6f7a8b9c0d1e"
	issueA  = "9fd871a2-d554-467f-85b9-beff2dcd2ab2"
	issueB  = "1c2d3e4f-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
)

func body(id, title string, rev int64, base string) Draft {
	return Draft{Kind: IssueBody, ServiceID: "a29ca783-fb66-42a1-befe-9149289cc659", ProjectID: project, RecordID: id, IssueID: id,
		Title: title, Revision: rev, Base: base, Claim: ClaimEdit}
}

// A draft is a file holding exactly the content, named after the title and
// the record, in its project's directory, with a sidecar recording the
// identity, the revision and the claim state.
func TestCreateWritesExactlyTheContentAndASidecar(t *testing.T) {
	s := New(t.TempDir())
	content := "# Plan\n\nNo frontmatter, kept\r\nexactly\twith tabs and no final newline"
	d, err := s.Create(body(issueA, "Fix the parser: ünïcode & spaces!", 3, "old"), content)
	if err != nil {
		t.Fatal(err)
	}
	if d.File != "fix-the-parser-n-code-spaces--9fd871a2.md" || d.Dir() != filepath.Join(s.Root, project) || d.Path() != filepath.Join(d.Dir(), d.File) {
		t.Fatalf("file %q in %q", d.File, d.Dir())
	}
	got, err := os.ReadFile(d.Path())
	if err != nil || string(got) != content {
		t.Fatalf("file content %q, %v", got, err)
	}
	if read, err := s.Read(d); err != nil || read != content {
		t.Fatalf("read %q %v", read, err)
	}
	var side Draft
	raw, err := os.ReadFile(filepath.Join(d.Dir(), ".lit", "issue_body-"+issueA+".json"))
	if err == nil {
		err = protocol.Decode(raw, &side)
	}
	if err != nil {
		t.Fatal(err)
	}
	if side.SchemaVersion != 1 || side.RecordID != issueA || side.IssueID != issueA || side.Revision != 3 || side.Base != "old" ||
		side.Claim != ClaimEdit || side.File != d.File || side.ProjectID != project || side.CreatedAt == "" {
		t.Fatalf("sidecar %+v", side)
	}
	if found, ok, err := s.Find(project, IssueBody, issueA); err != nil || !ok || found.Path() != d.Path() || found.Revision != 3 {
		t.Fatalf("find %+v %v %v", found, ok, err)
	}
	if _, ok, err := s.Find(project, IssueBody, issueB); err != nil || ok {
		t.Fatalf("find of another record: %v %v", ok, err)
	}
	if info, err := os.Stat(d.Path()); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("draft file is not private: %v %v", info.Mode(), err)
	}
}

// An existing draft is never overwritten: neither by a second draft of the
// same record, nor over a stray file that has the draft's name.
func TestCreateNeverOverwrites(t *testing.T) {
	s := New(t.TempDir())
	d, err := s.Create(body(issueA, "Same", 1, ""), "mine")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(body(issueA, "Renamed meanwhile", 2, ""), "theirs"); litclient.Code(err) != "draft_exists" {
		t.Fatalf("second draft: %v", err)
	}
	if got, _ := s.Read(d); got != "mine" {
		t.Fatalf("draft overwritten: %q", got)
	}
	stray := filepath.Join(d.Dir(), "same--1c2d3e4f.md")
	if err := os.WriteFile(stray, []byte("not a draft"), 0o600); err != nil {
		t.Fatal(err)
	}
	other, err := s.Create(body(issueB, "Same", 1, ""), "other")
	if err != nil {
		t.Fatal(err)
	}
	if other.File != "same--"+issueB+".md" {
		t.Fatalf("collision file %q", other.File)
	}
	if b, _ := os.ReadFile(stray); string(b) != "not a draft" {
		t.Fatalf("stray file overwritten: %q", b)
	}
	for _, bad := range []Draft{body("../escape", "x", 1, ""), {Kind: IssueBody, ProjectID: "..", RecordID: issueA}, {Kind: "", ProjectID: project, RecordID: issueA}} {
		if _, err := s.Create(bad, "x"); err == nil {
			t.Errorf("invalid draft %+v accepted", bad)
		}
	}
}

// Listing reads sidecars only: editor swap, backup and lock files, stray
// Markdown files and unreadable sidecars do not become drafts. Discarding
// removes the draft's file, its own leftovers and its sidecar, and leaves
// other drafts and files alone.
func TestListIgnoresEditorFilesAndDiscardRemovesTheDraftsOwn(t *testing.T) {
	s := New(t.TempDir())
	a, err := s.Create(body(issueA, "Alpha", 1, ""), "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(body(issueB, "Beta", 2, ""), "b")
	if err != nil {
		t.Fatal(err)
	}
	own := []string{"." + a.File + ".swp", "." + a.File + ".swo", a.File + "~", "#" + a.File + "#", ".#" + a.File, a.File + ".bak", a.File + ".orig"}
	others := []string{"." + b.File + ".swp", "notes.md", "4913", "alpha--9fd871a2.md.tmp"}
	for _, f := range append(slices.Clone(own), others...) {
		if err := os.WriteFile(filepath.Join(a.Dir(), f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(a.Dir(), ".lit", "issue_body-"+protocol.UUID()+".json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(project)
	if err == nil || !strings.Contains(err.Error(), "issue_body-") {
		t.Errorf("an unreadable sidecar is not reported: %v", err)
	}
	if len(got) != 2 || got[0].RecordID != issueA || got[1].RecordID != issueB {
		t.Fatalf("listed %+v", got)
	}
	all, _ := s.ListAll()
	if len(all) != 2 {
		t.Fatalf("all projects: %+v", all)
	}
	if err := s.Discard(a); err != nil {
		t.Fatal(err)
	}
	for _, f := range append(own, a.File, ".lit/issue_body-"+issueA+".json") {
		if _, err := os.Lstat(filepath.Join(a.Dir(), f)); !os.IsNotExist(err) {
			t.Errorf("%s kept: %v", f, err)
		}
	}
	for _, f := range append(others, b.File) {
		if _, err := os.Lstat(filepath.Join(a.Dir(), f)); err != nil {
			t.Errorf("%s removed: %v", f, err)
		}
	}
	if left, _ := s.List(project); len(left) != 1 || left[0].RecordID != issueB {
		t.Fatalf("after discard %+v", left)
	}
	if _, ok, _ := s.Find(project, IssueBody, issueA); ok {
		t.Fatal("a discarded draft is still found")
	}
	if err := s.Discard(a); err != nil {
		t.Fatalf("discarding twice: %v", err)
	}
}

// Save records a reload (a new revision and base) and keeps the file; a
// missing draft file reads as draft_missing, and an empty store lists
// nothing.
func TestSaveKeepsTheFileAndAMissingFileIsReported(t *testing.T) {
	s := New(t.TempDir())
	if got, err := s.List(project); err != nil || len(got) != 0 {
		t.Fatalf("empty store: %v %v", got, err)
	}
	if got, err := s.ListAll(); err != nil || len(got) != 0 {
		t.Fatalf("empty store, all projects: %v %v", got, err)
	}
	d, err := s.Create(body(issueA, "Alpha", 1, "one"), "edited")
	if err != nil {
		t.Fatal(err)
	}
	d.Revision, d.Base, d.Claim = 4, "four", ClaimExplicit
	if _, err := s.Save(d); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Find(project, IssueBody, issueA)
	if got.Revision != 4 || got.Base != "four" || got.Claim != ClaimExplicit || got.CreatedAt != d.CreatedAt {
		t.Fatalf("saved %+v", got)
	}
	if c, _ := s.Read(got); c != "edited" {
		t.Fatalf("content after save %q", c)
	}
	if err := os.Remove(d.Path()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(d); litclient.Code(err) != "draft_missing" {
		t.Fatalf("missing file: %v", err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Fix the parser":                       "fix-the-parser",
		"  --Weird   / title?? ":               "weird-title",
		"日本語":                                  "draft",
		strings.Repeat("abcdefghij", 10) + "!": "abcdefghijabcdefghijabcdefghijabcdefghijabcdefgh",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
