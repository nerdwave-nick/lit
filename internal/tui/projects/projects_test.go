package projects

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
)

func page(next string, titles ...string) litclient.Page[protocol.Project] {
	p := litclient.Page[protocol.Project]{}
	for _, t := range titles {
		p.Items = append(p.Items, protocol.Project{SchemaVersion: 1, ID: "id-" + t, Title: t})
	}
	if next != "" {
		p.NextCursor = &next
	}
	return p
}

func lines(m *Model) []string { return strings.Split(ansi.Strip(m.View()), "\n") }

func fits(t *testing.T, m *Model, w, h int) {
	t.Helper()
	ls := strings.Split(m.View(), "\n")
	if len(ls) != h {
		t.Fatalf("%d lines, want %d", len(ls), h)
	}
	for i, l := range ls {
		if cells.Width(cells.Default, l) != w {
			t.Fatalf("line %d: %d cells, want %d: %q", i, cells.Width(cells.Default, l), w, ansi.Strip(l))
		}
	}
}

// The list loads page by page: reaching the end asks for the next page with
// its cursor, once, and choosing returns the project.
func TestSwitcherPagesAndChooses(t *testing.T) {
	m := New()
	m.SetSize(30, 8)
	m.Reset("id-test/b")
	if !strings.Contains(strings.Join(lines(m), "\n"), "Loading") {
		t.Fatalf("no loading row:\n%s", strings.Join(lines(m), "\n"))
	}
	m.AddPage(page("c1", "test/a", "test/b"), nil)
	fits(t, m, 30, 8)
	view := strings.Join(lines(m), "\n")
	if !strings.Contains(view, "test/a") || !strings.Contains(view, "More projects") || !strings.Contains(view, "current") {
		t.Fatalf("first page:\n%s", view)
	}
	if got := m.Selected(); got != 1 {
		t.Fatalf("the current project is not preselected: %d", got)
	}
	r := m.Do(keys.Down) // onto "More projects…"
	if r.More == nil || *r.More != "c1" {
		t.Fatalf("reaching the end does not load more: %+v", r)
	}
	if r := m.Do(keys.Open); r.More != nil || r.Chosen != nil {
		t.Fatalf("a second request while loading: %+v", r)
	}
	m.AddPage(page("", "test/c"), nil)
	if strings.Contains(strings.Join(lines(m), "\n"), "More projects") {
		t.Fatal("a complete list still offers more")
	}
	if r := m.Do(keys.Open); r.Chosen == nil || r.Chosen.Title != "test/c" {
		t.Fatalf("choose: %+v", r)
	}
	m.Do(keys.Top)
	if r := m.Do(keys.Open); r.Chosen == nil || r.Chosen.Title != "test/a" {
		t.Fatalf("choose the first: %+v", r)
	}
	if r := m.Do(keys.Cancel); !r.Cancel {
		t.Fatal("cancel")
	}
}

// Long lists scroll with the selection and every line fits; titles are
// sanitized.
func TestSwitcherScrollsAndSanitizes(t *testing.T) {
	m := New()
	m.SetSize(24, 6)
	m.Reset("")
	var titles []string
	for i := 0; i < 40; i++ {
		titles = append(titles, fmt.Sprintf("test/project-%02d-with-a-long-name", i))
	}
	titles[3] = "test/esc\x1b[2J"
	m.AddPage(page("", titles...), nil)
	m.Do(keys.Bottom)
	fits(t, m, 24, 6)
	if !strings.Contains(strings.Join(lines(m), "\n"), "39") || strings.Contains(m.View(), "\x1b[2J") {
		t.Fatalf("bottom:\n%s", strings.Join(lines(m), "\n"))
	}
	m.Do(keys.PageUp)
	m.Do(keys.Top)
	m.Do(keys.Down)
	m.Do(keys.Down)
	m.Do(keys.Down)
	if !strings.Contains(strings.Join(lines(m), "\n"), "␛") {
		t.Fatalf("escape not shown as a marker:\n%s", strings.Join(lines(m), "\n"))
	}
	m.SetPlain(true)
	if !strings.Contains(strings.Join(lines(m), "\n"), "> test/esc") {
		t.Fatalf("plain marker:\n%s", strings.Join(lines(m), "\n"))
	}
}

func TestSwitcherEmptyAndErrors(t *testing.T) {
	m := New()
	m.SetSize(40, 4)
	m.Reset("")
	m.AddPage(page(""), nil)
	if !strings.Contains(strings.Join(lines(m), "\n"), "No projects") {
		t.Fatalf("empty:\n%s", strings.Join(lines(m), "\n"))
	}
	if r := m.Do(keys.Open); r.Chosen != nil {
		t.Fatal("chose from an empty list")
	}
	m.Reset("")
	m.AddPage(page("c1", "test/a"), nil)
	m.Do(keys.Down)
	m.AddPage(litclient.Page[protocol.Project]{}, protocol.E(400, "invalid_cursor", "stale"))
	view := strings.Join(lines(m), "\n")
	if !strings.Contains(view, "invalid_cursor") || !strings.Contains(view, "test/a") {
		t.Fatalf("error:\n%s", view)
	}
	if r := m.Do(keys.Open); r.More == nil {
		t.Fatalf("the error row does not retry: %+v", r)
	}
	m.AddPage(litclient.Page[protocol.Project]{}, errors.New("boom"))
	if !strings.Contains(strings.Join(lines(m), "\n"), "boom") {
		t.Fatal("plain error not shown")
	}
}

func TestSwitcherClickAndWheel(t *testing.T) {
	m := New()
	m.SetSize(30, 6)
	m.Reset("")
	m.AddPage(page("", "test/a", "test/b", "test/c"), nil)
	if r := m.Click(2); r.Chosen != nil || m.Selected() != 1 { // row 0 is the heading
		t.Fatalf("click selects: %+v %d", r, m.Selected())
	}
	if r := m.Click(2); r.Chosen == nil || r.Chosen.Title != "test/b" {
		t.Fatalf("clicking the selection chooses it: %+v", r)
	}
	m.Wheel(5)
	if m.Selected() != 2 {
		t.Fatalf("wheel: %d", m.Selected())
	}
}
