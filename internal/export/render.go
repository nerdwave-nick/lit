package export

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// renderer turns the loaded tracker into the files of the export tree.
type renderer struct{ t *tracker }

func newRenderer(t *tracker) *renderer { return &renderer{t} }

// writeAll renders every file except manifest.json and hands it to write,
// reading each record's history from snap as it goes.
func (r *renderer) writeAll(ctx context.Context, snap Snapshot, write func(name string, b []byte) error, m Manifest) error {
	if e := write("README.md", []byte(r.readme(m))); e != nil {
		return e
	}
	emit := func(name, text string, typ, id string, history func(string, []storage.HistoryEntry) (string, error)) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		hs, e := snap.History(ctx, typ, id, 0, 0)
		if e != nil {
			return e
		}
		if e = write(name, []byte(text)); e != nil {
			return e
		}
		h, e := history(historyFile(name), hs)
		if e != nil {
			return e
		}
		return write(historyFile(name), []byte(h))
	}
	for k := range r.t.projects {
		p := &r.t.projects[k]
		name := r.t.dir[p.ID] + "/project.md"
		if e := emit(name, r.project(p), storage.Projects, p.ID, func(from string, hs []storage.HistoryEntry) (string, error) {
			return r.recordHistory(from, "project", p.Title, name, storage.Projects, hs), nil
		}); e != nil {
			return e
		}
	}
	for k := range r.t.issues {
		i := &r.t.issues[k]
		if e := emit(r.t.issueFile[i.ID], r.issue(i), storage.Issues, i.ID, func(from string, hs []storage.HistoryEntry) (string, error) {
			return r.issueHistory(ctx, snap, from, i, hs)
		}); e != nil {
			return e
		}
	}
	for k := range r.t.milestones {
		m := &r.t.milestones[k]
		name := r.t.milestoneFile[m.ID]
		if e := emit(name, r.milestone(m), storage.Milestones, m.ID, func(from string, hs []storage.HistoryEntry) (string, error) {
			return r.recordHistory(from, "milestone", m.Title, name, storage.Milestones, hs), nil
		}); e != nil {
			return e
		}
	}
	return ctx.Err()
}

// issueLink is the relative path from the file from to an issue's file, or
// its lit: URI when the issue is not exported.
func (r *renderer) issueLink(from, id string) string {
	if f, ok := r.t.issueFile[id]; ok {
		return rel(from, f)
	}
	return "lit:issue:" + id
}

func (r *renderer) issueLinks(from string, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.issueLink(from, id))
	}
	return out
}

// issueRef is a Markdown link to an issue titled with its current title.
func (r *renderer) issueRef(from, id string) string {
	if i, ok := r.t.issue[id]; ok {
		return "[" + inline(i.Title) + "](" + r.issueLink(from, id) + ")"
	}
	return codeSpan(id)
}

func (r *renderer) issueRefs(from string, ids []string) string {
	refs := make([]string, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, r.issueRef(from, id))
	}
	return strings.Join(refs, ", ")
}

// body rewrites lit:issue links for the file from and ends the text with a
// line break.
func (r *renderer) body(from, text string) string {
	text = rewriteIssueLinks(text, func(id string) (string, bool) {
		f, ok := r.t.issueFile[id]
		return rel(from, f), ok
	})
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}

// linksBlock is the short clickable list under a title: label, then links.
func linksBlock(items [][2]string) string {
	var b strings.Builder
	b.WriteString("**Links**\n\n")
	for _, it := range items {
		if it[1] != "" {
			fmt.Fprintf(&b, "- %s: %s\n", it[0], it[1])
		}
	}
	return b.String()
}

func (r *renderer) issue(i *protocol.Issue) string {
	from := r.t.issueFile[i.ID]
	project := r.t.dir[i.ProjectID] + "/project.md"
	var f frontMatter
	f.str("id", i.ID)
	f.str("title", i.Title)
	f.str("state", i.State)
	f.str("project", rel(from, project))
	f.list("labels", i.Labels)
	f.optional("assignee", i.Assignee)
	parent := ""
	if i.ParentID != nil {
		f.str("parent", r.issueLink(from, *i.ParentID))
		parent = r.issueRef(from, *i.ParentID)
	}
	f.list("children", r.issueLinks(from, i.ChildIDs))
	f.list("blocks", r.issueLinks(from, i.Blocks))
	f.list("blocked_by", r.issueLinks(from, i.BlockedBy))
	f.list("related", r.issueLinks(from, i.Related))
	var milestones []string
	for _, id := range r.t.milestoneOf[i.ID] {
		milestones = append(milestones, rel(from, r.t.milestoneFile[id]))
	}
	f.list("milestones", milestones)
	f.str("created_at", i.CreatedAt)
	f.str("updated_at", i.UpdatedAt)
	f.int("revision", i.Revision)
	var b strings.Builder
	b.WriteString(f.String() + "\n# " + inline(i.Title) + "\n\n")
	b.WriteString(linksBlock([][2]string{
		{"Project", "[" + inline(r.t.project[i.ProjectID].Title) + "](" + rel(from, project) + ")"},
		{"Parent", parent},
		{"Children", r.issueRefs(from, i.ChildIDs)},
		{"Blocked by", r.issueRefs(from, i.BlockedBy)},
		{"Blocks", r.issueRefs(from, i.Blocks)},
		{"History", "[" + inline(historyFile(baseName(from))) + "](" + rel(from, historyFile(from)) + ")"},
	}))
	if text := r.body(from, i.Body); text != "" {
		b.WriteString("\n" + text)
	}
	if cs := r.t.comments[i.ID]; len(cs) > 0 {
		b.WriteString("\n## Comments\n")
		for _, c := range cs {
			fmt.Fprintf(&b, "\n### %s, %s\n\nComment %s", inline(c.Author), c.CreatedAt, codeSpan(c.ID))
			if c.Revision > 1 {
				fmt.Fprintf(&b, ", edited %s", c.UpdatedAt)
			}
			b.WriteString("\n")
			if text := r.body(from, demoteHeadings(c.Body)); text != "" {
				b.WriteString("\n" + text)
			}
		}
	}
	return b.String()
}

func baseName(p string) string { return p[strings.LastIndexByte(p, '/')+1:] }

// byCreation orders issues by creation, then ID.
func byCreation(is []*protocol.Issue) {
	sort.Slice(is, func(a, b int) bool {
		return is[a].CreatedAt < is[b].CreatedAt || is[a].CreatedAt == is[b].CreatedAt && is[a].ID < is[b].ID
	})
}

func (r *renderer) project(p *protocol.Project) string {
	from := r.t.dir[p.ID] + "/project.md"
	var f frontMatter
	f.str("id", p.ID)
	f.str("title", p.Title)
	f.list("repository_refs", p.RepositoryRefs)
	f.str("created_at", p.CreatedAt)
	f.str("updated_at", p.UpdatedAt)
	f.int("revision", p.Revision)
	var b strings.Builder
	b.WriteString(f.String() + "\n" + linksBlock([][2]string{{"History", "[project.history.md](project.history.md)"}}))
	if text := r.body(from, p.Description); text != "" {
		b.WriteString("\n" + text)
	}
	var open, closed []*protocol.Issue
	for k := range r.t.issues {
		if i := &r.t.issues[k]; i.ProjectID == p.ID && i.State == "closed" {
			closed = append(closed, i)
		} else if i.ProjectID == p.ID {
			open = append(open, i)
		}
	}
	for _, group := range []struct {
		heading string
		issues  []*protocol.Issue
	}{{"Open issues", open}, {"Closed issues", closed}} {
		if len(group.issues) == 0 {
			continue
		}
		byCreation(group.issues)
		fmt.Fprintf(&b, "\n## %s (generated index)\n\n", group.heading)
		for _, i := range group.issues {
			b.WriteString("- " + r.issueRef(from, i.ID))
			for _, l := range i.Labels {
				b.WriteString(" " + codeSpan(l))
			}
			b.WriteString("\n")
		}
	}
	var milestones []string
	for _, m := range r.t.milestones {
		if m.ProjectID == p.ID {
			milestones = append(milestones, "- ["+inline(m.Title)+"]("+rel(from, r.t.milestoneFile[m.ID])+")\n")
		}
	}
	if len(milestones) > 0 {
		sort.Strings(milestones)
		b.WriteString("\n## Milestones (generated index)\n\n" + strings.Join(milestones, ""))
	}
	return b.String()
}

func (r *renderer) milestone(m *protocol.Milestone) string {
	from := r.t.milestoneFile[m.ID]
	project := r.t.dir[m.ProjectID] + "/project.md"
	var f frontMatter
	f.str("id", m.ID)
	f.str("title", m.Title)
	f.str("project", rel(from, project))
	f.list("members", r.issueLinks(from, m.IssueIDs))
	f.str("created_at", m.CreatedAt)
	f.str("updated_at", m.UpdatedAt)
	f.int("revision", m.Revision)
	var b strings.Builder
	b.WriteString(f.String() + "\n# " + inline(m.Title) + "\n\n")
	b.WriteString(linksBlock([][2]string{
		{"Project", "[" + inline(r.t.project[m.ProjectID].Title) + "](" + rel(from, project) + ")"},
		{"History", "[" + inline(historyFile(baseName(from))) + "](" + rel(from, historyFile(from)) + ")"},
	}))
	if text := r.body(from, m.Body); text != "" {
		b.WriteString("\n" + text)
	}
	if len(m.IssueIDs) > 0 {
		b.WriteString("\n## Members\n\n")
		for _, id := range m.IssueIDs {
			b.WriteString("- " + r.issueRef(from, id))
			if i, ok := r.t.issue[id]; ok {
				b.WriteString(" (" + i.State + ")")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (r *renderer) readme(m Manifest) string {
	var b strings.Builder
	b.WriteString("# lit export\n\nWritten by lit-server. Edits are overwritten by the next export and never read back.\n\n")
	fmt.Fprintf(&b, "- Service ID: %s\n- Exported at: %s\n", codeSpan(m.ServiceID), m.ExportedAt)
	if t := m.LastTransaction; t != nil {
		fmt.Fprintf(&b, "- Last transaction: %s at %s\n", codeSpan(t.RequestHash), t.Timestamp)
	} else {
		b.WriteString("- Last transaction: none\n")
	}
	if len(r.t.projects) > 0 {
		b.WriteString("\n## Projects\n\n")
		var lines []string
		for _, p := range r.t.projects {
			lines = append(lines, "- ["+inline(p.Title)+"]("+r.t.dir[p.ID]+"/project.md)\n")
		}
		sort.Strings(lines)
		b.WriteString(strings.Join(lines, ""))
	}
	return b.String()
}
