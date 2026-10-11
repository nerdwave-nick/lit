package export_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v3"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/export"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

var update = flag.Bool("update", false, "rewrite the golden export tree")

var exportTime = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// run exports the database's current state into dir at the given time.
func run(t *testing.T, db *storage.DB, dir string, when time.Time) export.Manifest {
	t.Helper()
	ctx := context.Background()
	snap, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	m, e := export.Export(ctx, snap, dir, export.Options{ServiceID: fixtureService, Time: when})
	if e != nil {
		t.Fatal(e)
	}
	return m
}

// tree reads every file below dir as slash-separated relative paths.
func tree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	e := filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		r, _ := filepath.Rel(dir, p)
		if d.Type()&fs.ModeSymlink != 0 {
			out[filepath.ToSlash(r)] = []byte("symlink")
			return nil
		}
		b, e := os.ReadFile(p)
		out[filepath.ToSlash(r)] = b
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func names(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestExportMatchesTheGoldenTree(t *testing.T) {
	db := fixture(t)
	dir := filepath.Join(t.TempDir(), "export")
	m := run(t, db, dir, exportTime)
	got := tree(t, dir)
	golden := filepath.Join("testdata", "golden")
	if *update {
		if e := os.RemoveAll(golden); e != nil {
			t.Fatal(e)
		}
		for name, b := range got {
			p := filepath.Join(golden, filepath.FromSlash(name))
			if e := os.MkdirAll(filepath.Dir(p), 0o755); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(p, b, 0o644); e != nil {
				t.Fatal(e)
			}
		}
	}
	want := tree(t, golden)
	if g, w := strings.Join(names(got), "\n"), strings.Join(names(want), "\n"); g != w {
		t.Fatalf("files\n%s\nwant\n%s", g, w)
	}
	for _, name := range names(want) {
		if !bytes.Equal(got[name], want[name]) {
			t.Errorf("%s differs from the golden file:\n%s", name, got[name])
		}
	}
	wantCounts := export.Counts{Projects: 2, Issues: 4, Comments: 2, Milestones: 1}
	if m.Counts != wantCounts || m.ServiceID != fixtureService || m.LastTransaction == nil || m.LastTransaction.Timestamp != at(9) {
		t.Fatalf("manifest %+v", m)
	}
}

func TestExportIsDeterministic(t *testing.T) {
	db := fixture(t)
	root := t.TempDir()
	run(t, db, filepath.Join(root, "one"), exportTime)
	run(t, db, filepath.Join(root, "two"), exportTime.Add(time.Hour))
	one, two := tree(t, filepath.Join(root, "one")), tree(t, filepath.Join(root, "two"))
	// Exporting again into the same directory replaces it with the same bytes.
	run(t, db, filepath.Join(root, "one"), exportTime)
	again := tree(t, filepath.Join(root, "one"))
	if strings.Join(names(one), "\n") != strings.Join(names(two), "\n") || len(one) != len(again) {
		t.Fatalf("file sets differ: %v %v %v", names(one), names(two), names(again))
	}
	stamp := func(when time.Time) string { return when.Format(storage.TimeFormat) }
	for _, name := range names(one) {
		if !bytes.Equal(one[name], again[name]) {
			t.Errorf("%s differs on a repeated export", name)
		}
		a, b := string(one[name]), string(two[name])
		if name == "README.md" || name == "manifest.json" {
			if !strings.Contains(a, stamp(exportTime)) || !strings.Contains(b, stamp(exportTime.Add(time.Hour))) {
				t.Errorf("%s lacks the export time", name)
			}
			a = strings.ReplaceAll(a, stamp(exportTime), "TIME")
			b = strings.ReplaceAll(b, stamp(exportTime.Add(time.Hour)), "TIME")
		}
		if a != b {
			t.Errorf("%s differs between exports at different times", name)
		}
	}
}

// links lists the link and image destinations of a Markdown document.
func links(source []byte) []string {
	var out []string
	doc := goldmark.New().Parser().Parse(text.NewReader(source))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch l := n.(type) {
		case *ast.Link:
			out = append(out, string(l.Destination))
		case *ast.Image:
			out = append(out, string(l.Destination))
		}
		return ast.WalkContinue, nil
	})
	return out
}

// frontMatter splits a document into its parsed front matter and the rest.
func frontMatter(t *testing.T, name string, doc []byte) (map[string]any, []byte) {
	t.Helper()
	s, ok := strings.CutPrefix(string(doc), "---\n")
	if !ok {
		return nil, doc
	}
	head, rest, ok := strings.Cut(s, "\n---\n")
	if !ok {
		t.Fatalf("%s: unterminated front matter", name)
	}
	values := map[string]any{}
	if e := yaml.Unmarshal([]byte(head), &values); e != nil {
		t.Fatalf("%s: front matter is not YAML: %v", name, e)
	}
	return values, []byte(rest)
}

func TestExportLinksResolve(t *testing.T) {
	db := fixture(t)
	dir := filepath.Join(t.TempDir(), "export")
	run(t, db, dir, exportTime)
	files := tree(t, dir)
	var broken []string
	resolve := func(name, dest string) {
		if dest == "" || strings.HasPrefix(dest, "#") || strings.Contains(strings.Split(dest, "/")[0], ":") {
			return // anchors and URIs with a scheme
		}
		target := path.Join(path.Dir(name), strings.SplitN(dest, "#", 2)[0])
		if _, ok := files[target]; !ok {
			broken = append(broken, name+" -> "+dest)
		}
	}
	checked := 0
	for _, name := range names(files) {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		values, body := frontMatter(t, name, files[name])
		for _, key := range []string{"project", "parent", "children", "blocks", "blocked_by", "related", "milestones", "members"} {
			switch v := values[key].(type) {
			case string:
				resolve(name, v)
				checked++
			case []any:
				for _, item := range v {
					resolve(name, item.(string))
					checked++
				}
			}
		}
		for _, dest := range links(body) {
			resolve(name, dest)
			checked++
		}
	}
	// Only the author's own relative link, which the export leaves unchanged,
	// points outside the tree.
	want := []string{"feat/lit-export/issues/aaaaaaaa-plan-the-export-layout-naming.md -> docs/notes.md"}
	if strings.Join(broken, "\n") != strings.Join(want, "\n") {
		t.Fatalf("broken links:\n%s\nwant:\n%s", strings.Join(broken, "\n"), strings.Join(want, "\n"))
	}
	if checked < 40 {
		t.Fatalf("checked only %d links", checked)
	}
	// lit:issue links to exported issues are all rewritten outside code.
	for _, name := range names(files) {
		if strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".history.md") {
			for _, dest := range links(files[name]) {
				if strings.HasPrefix(dest, "lit:") {
					t.Errorf("%s keeps link %s", name, dest)
				}
			}
		}
	}
}

func TestExportFrontMatterParsesAsYAML(t *testing.T) {
	db := fixture(t)
	dir := filepath.Join(t.TempDir(), "export")
	run(t, db, dir, exportTime)
	files := tree(t, dir)
	ids := map[string]string{}
	for _, name := range names(files) {
		if !strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".history.md") || name == "README.md" {
			continue
		}
		values, _ := frontMatter(t, name, files[name])
		if values == nil {
			t.Fatalf("%s has no front matter", name)
		}
		id, _ := values["id"].(string)
		ids[id], _ = values["title"].(string)
		if _, ok := values["claim"]; ok {
			t.Errorf("%s exports a claim", name)
		}
		for k, v := range values {
			if v == nil || v == "" {
				t.Errorf("%s: empty field %s", name, k)
			}
			if l, ok := v.([]any); ok && len(l) == 0 {
				t.Errorf("%s: empty list %s", name, k)
			}
		}
	}
	want := map[string]string{p1: "feat/lit-export", p2: "fix/other-project", issueA: "Plan the export: layout & naming", issueB: "Write the front-matter writer",
		issueC: "Ünïcode title — with a very long name that needs cutting", issueX: "Cross-project issue", milestone1: "M1: Export"}
	if len(ids) != len(want) {
		t.Fatalf("front matter ids %v", ids)
	}
	for id, title := range want {
		if ids[id] != title {
			t.Errorf("%s title %q, want %q", id, ids[id], title)
		}
	}
	var m export.Manifest
	if e := json.Unmarshal(files["manifest.json"], &m); e != nil || m.ExportedAt != exportTime.Format(storage.TimeFormat) {
		t.Fatalf("manifest %s: %v", files["manifest.json"], e)
	}
}

// interrupting reads through a snapshot, but commits a write the first time
// the export lists records.
type interrupting struct {
	*storage.ReadTx
	t    *testing.T
	db   *storage.DB
	done bool
}

func (s *interrupting) List(ctx context.Context, q storage.ListQuery) ([]any, error) {
	if !s.done {
		s.done = true
		p := project("33333333-0000-4000-8000-000000000003", "feat/late", "", at(30))
		commit(s.t, s.db, nick, at(30), p)
	}
	return s.ReadTx.List(ctx, q)
}

func TestExportReadsOneSnapshot(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	snap, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	dir := filepath.Join(t.TempDir(), "export")
	m, e := export.Export(ctx, &interrupting{ReadTx: snap, t: t, db: db}, dir, export.Options{ServiceID: fixtureService, Time: exportTime})
	if e != nil {
		t.Fatal(e)
	}
	if m.Counts.Projects != 2 || m.LastTransaction.Timestamp != at(9) {
		t.Fatalf("the export saw a later commit: %+v", m)
	}
	if _, e = os.Stat(filepath.Join(dir, "feat", "late")); !os.IsNotExist(e) {
		t.Fatalf("the export wrote a later project: %v", e)
	}
	golden := tree(t, filepath.Join("testdata", "golden"))
	got := tree(t, dir)
	for name := range golden {
		if !bytes.Equal(golden[name], got[name]) {
			t.Errorf("%s differs from the golden export", name)
		}
	}
}
