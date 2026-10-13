package history

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// kind is what a line of the view shows.
type kind int

const (
	text    kind = iota // plain text
	heading             // an entry's first line
	kept                // an unchanged body line
	removed             // a body line before
	added               // a body line after
	gap                 // elided unchanged body lines
)

// line is one logical line of the view, before wrapping: its kind, indent
// and sanitized text.
type line struct {
	kind   kind
	indent int
	text   string
}

// stampFormat is how lit-server writes history timestamps.
const stampFormat = "2006-01-02T15:04:05.000000Z"

// bookkeeping fields change with every entry or say nothing a person needs;
// the heading already gives the revision and time.
var bookkeeping = map[string]bool{"schema_version": true, "id": true, "revision": true, "created_at": true, "updated_at": true}

// creation fields only place a new record; its title and detail name it.
var placement = map[string]bool{"project_id": true, "issue_id": true}

var singular = map[string]string{"issues": "issue", "comments": "comment", "projects": "project", "milestones": "milestone"}

// entryLines lays out entry e, the nth of its record's history: who, when and which revisions; the operations and the other
// records the transaction changed; each changed field's JSON value before
// and after; and the body as a line diff with context. Every piece of
// tracker text is sanitized.
func entryLines(n int, e litclient.HistoryEntry) []line {
	created := e.BeforeRevision == 0
	head := []string{"#" + itoa(int64(n))}
	if created {
		head = append(head, "created", "rev "+itoa(e.Revision))
	} else {
		head = append(head, "rev "+itoa(e.BeforeRevision)+" → "+itoa(e.Revision))
	}
	who := sanitize.Title(e.Intent.Actor.Name) + " (" + sanitize.Title(e.Intent.Actor.Kind) + ")"
	head = append(head, when(e.Timestamp), who, "client "+short(e.Intent.Actor.ClientID))
	out := []line{{heading, 0, strings.Join(head, " · ")}}

	var ops []string
	for _, op := range e.Intent.Operations {
		s := name(op.Type) + " " + sanitize.Title(op.Kind)
		if op.ID != e.OwnerID {
			s += " " + short(op.ID)
		}
		ops = append(ops, s)
	}
	if len(ops) == 0 {
		ops = []string{sanitize.Title(e.Intent.Operation)}
	}
	out = append(out, line{text, 2, strings.Join(ops, ", ") + " · request " + short(e.RequestHash)})
	var also []string
	for _, r := range e.Results {
		if r.ID == e.OwnerID {
			continue
		}
		revs := "rev " + itoa(r.BeforeRevision) + " → " + itoa(r.Revision)
		if r.BeforeRevision == 0 {
			revs = "created, rev " + itoa(r.Revision)
		}
		also = append(also, name(r.Type)+" "+short(r.ID)+" ("+revs+")")
	}
	if len(also) > 0 {
		out = append(out, line{text, 2, "also changed: " + strings.Join(also, ", ")})
	}

	fields := make([]string, 0, len(e.Differences))
	for k := range e.Differences {
		fields = append(fields, k)
	}
	slices.Sort(fields)
	for _, k := range fields {
		d := e.Differences[k]
		if bookkeeping[k] || created && (placement[k] || empty(d.After)) {
			continue
		}
		s := sanitize.Title(k) + ": " + value(d.After)
		if !created {
			s = sanitize.Title(k) + ": " + value(d.Before) + " → " + value(d.After)
		}
		out = append(out, line{text, 2, s})
	}

	for _, h := range e.BodyHunks {
		out = append(out, line{text, 2, "body:"})
		for _, ed := range hunks(diffLines(sanitize.Body(h.Before), sanitize.Body(h.After)), ContextLines) {
			prefix := map[kind]string{kept: "  ", removed: "- ", added: "+ ", gap: "…"}[ed.kind]
			out = append(out, line{ed.kind, 4, prefix + ed.text})
		}
	}
	return out
}

// value is a field's JSON value, compact and sanitized.
func value(v json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, v) != nil {
		return sanitize.Title(string(v))
	}
	return sanitize.Title(b.String())
}

// empty reports a value that says nothing at creation: null, "", [] or {}.
func empty(v json.RawMessage) bool {
	switch string(bytes.TrimSpace(v)) {
	case "", "null", `""`, "[]", "{}":
		return true
	}
	return false
}

// when is a history timestamp in UTC to the second, or the sanitized
// timestamp when it does not parse.
func when(stamp string) string {
	t, err := time.Parse(stampFormat, stamp)
	if err != nil {
		return sanitize.Title(stamp)
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func name(typ string) string {
	if s, ok := singular[typ]; ok {
		return s
	}
	return sanitize.Title(typ)
}

// short is the first eight characters of an ID or hash.
func short(id string) string {
	if len(id) > 8 {
		id = id[:8]
	}
	return sanitize.Title(id)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
