package export

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
	"github.com/nerdwave-nick/lit/internal/textdiff"
)

// hiddenFields never appear in a history entry: the heading shows the
// revision and time, and identity fields never change.
var hiddenFields = map[string]bool{"revision": true, "updated_at": true, "created_at": true, "schema_version": true, "id": true}

// computedFields are the membership lists computed on read. Old entries that
// changed them are kept by the server but carry nothing for a reader.
var computedFields = map[string]map[string]bool{
	storage.Projects: {"issue_ids": true},
	storage.Issues:   {"child_ids": true, "comment_ids": true},
}

// linkFields are fields holding issue IDs (or a project ID) that render as
// links to the records' files.
var linkFields = map[string]map[string]bool{
	storage.Issues:     {"parent_id": true, "blocks": true, "blocked_by": true, "related": true, "project_id": true},
	storage.Comments:   {"issue_id": true},
	storage.Milestones: {"issue_ids": true, "project_id": true},
}

func (r *renderer) issueHistory(ctx context.Context, snap Snapshot, from string, i *protocol.Issue, hs []storage.HistoryEntry) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# History: %s\n\nRevisions oldest first. Current issue: [%s](%s).\n\n## Issue\n", inline(i.Title), inline(baseName(r.t.issueFile[i.ID])), rel(from, r.t.issueFile[i.ID]))
	b.WriteString(r.entries(from, storage.Issues, hs, "###"))
	for _, c := range r.t.comments[i.ID] {
		ch, e := snap.History(ctx, storage.Comments, c.ID, 0, 0)
		if e != nil {
			return "", e
		}
		fmt.Fprintf(&b, "\n## Comment %s by %s\n", codeSpan(c.ID), inline(c.Author))
		b.WriteString(r.entries(from, storage.Comments, ch, "###"))
	}
	return b.String(), nil
}

func (r *renderer) recordHistory(from, kind, title, current, typ string, hs []storage.HistoryEntry) string {
	return fmt.Sprintf("# History: %s\n\nRevisions oldest first. Current %s: [%s](%s).\n", inline(title), kind, inline(baseName(current)), rel(from, current)) +
		r.entries(from, typ, hs, "##")
}

// entries renders revisions oldest first, each under a heading of the given
// level: time, actor, request hash, field changes and the body change. A
// creation lists its fields and the body's line count; later body changes
// are unified diffs. Entries that changed nothing but computed membership
// lists are omitted.
func (r *renderer) entries(from, typ string, hs []storage.HistoryEntry, level string) string {
	var b strings.Builder
	body := "body"
	if typ == storage.Projects {
		body = "description"
	}
	for _, h := range hs {
		created := h.BeforeRevision == 0
		var lines []string
		keys := make([]string, 0, len(h.Differences))
		for k := range h.Differences {
			if !hiddenFields[k] && !computedFields[typ][k] && k != body {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			if l := r.change(from, typ, k, h.Differences[k], created); l != "" {
				lines = append(lines, l)
			}
		}
		var diff string
		if created {
			text := ""
			if len(h.BodyHunks) > 0 {
				text = h.BodyHunks[len(h.BodyHunks)-1].After
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", body, lineCount(text)))
		} else {
			for _, hunk := range h.BodyHunks {
				if d := textdiff.Diff(hunk.Before, hunk.After); len(d) > 0 {
					diff += "\n" + fenced("diff", d)
				}
			}
		}
		if !created && len(lines) == 0 && diff == "" {
			continue
		}
		what := "updated"
		if created {
			what = "created"
		}
		actor := h.Intent.Actor
		fmt.Fprintf(&b, "\n%s r%d %s, %s, %s (%s)\n\nRequest %s\n", level, h.Revision, what, h.Timestamp, inline(actor.Name), inline(actor.Kind), codeSpan(h.RequestHash))
		if len(lines) > 0 {
			b.WriteString("\n" + strings.Join(lines, "\n") + "\n")
		}
		b.WriteString(diff)
	}
	return b.String()
}

func lineCount(s string) string {
	n := strings.Count(s, "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		n++
	}
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

// change renders one field change: a scalar as before → after, a list as
// the added and removed elements; a creation shows only the value, and
// omits empty ones.
func (r *renderer) change(from, typ, key string, d storage.FieldDifference, created bool) string {
	link := linkFields[typ][key]
	var before, after []string
	if json.Unmarshal(d.After, &after) == nil && after != nil && (isNull(d.Before) || json.Unmarshal(d.Before, &before) == nil) {
		show := func(vs []string) string {
			out := make([]string, len(vs))
			for k, v := range vs {
				out[k] = r.value(from, v, link)
			}
			return strings.Join(out, ", ")
		}
		if created {
			if len(after) == 0 {
				return ""
			}
			return fmt.Sprintf("- %s: %s", key, show(after))
		}
		added, removed := setDifference(after, before), setDifference(before, after)
		var parts []string
		if len(added) > 0 {
			parts = append(parts, "added "+show(added))
		}
		if len(removed) > 0 {
			parts = append(parts, "removed "+show(removed))
		}
		if len(parts) == 0 {
			return ""
		}
		return fmt.Sprintf("- %s: %s", key, strings.Join(parts, "; "))
	}
	scalar := func(m json.RawMessage) string {
		var s string
		if link && json.Unmarshal(m, &s) == nil {
			return r.value(from, s, true)
		}
		if isNull(m) && link {
			return "none"
		}
		return codeSpan(displayJSON(m))
	}
	if created {
		if isNull(d.After) || string(d.After) == `""` {
			return ""
		}
		return fmt.Sprintf("- %s: %s", key, scalar(d.After))
	}
	return fmt.Sprintf("- %s: %s → %s", key, scalar(d.Before), scalar(d.After))
}

// value renders one field element: a link to the record an ID names, or the
// text as a code span.
func (r *renderer) value(from, v string, link bool) string {
	if link {
		if _, ok := r.t.issue[v]; ok {
			return r.issueRef(from, v)
		}
		if p, ok := r.t.project[v]; ok {
			return "[" + inline(p.Title) + "](" + rel(from, r.t.dir[v]+"/project.md") + ")"
		}
	}
	return codeSpan(v)
}

// setDifference lists the elements of a that b lacks, in a's order.
func setDifference(a, b []string) []string {
	in := map[string]bool{}
	for _, v := range b {
		in[v] = true
	}
	var out []string
	for _, v := range a {
		if !in[v] {
			out = append(out, v)
		}
	}
	return out
}

// displayJSON is a JSON value on one line, without the HTML escapes the
// stored differences carry (so & shows as &, not \u0026).
func displayJSON(m json.RawMessage) string {
	d := json.NewDecoder(bytes.NewReader(m))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return string(m)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return string(m)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func isNull(m json.RawMessage) bool { return len(m) == 0 || string(m) == "null" }
