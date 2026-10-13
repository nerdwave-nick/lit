package issue

import (
	"strconv"
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/detail"
	"github.com/nerdwave-nick/lit/internal/tui/recovery"
)

// Section notes (Markdown) shown while a detail loads or when it cannot.
const (
	Loading     = "*Loading the body and comments…*"
	NoBody      = "*No description.*"
	NoComments  = "*No comments.*"
	PagedNote   = "*Read in pages: comments may have changed between pages.*"
	issueFailed = "*Could not load the issue: "
	pagesFailed = "*Comments stopped loading: "
)

// Sections returns the detail's sections after the metadata of issue id: its
// body, a comments heading and one section per comment, each rendered on its
// own so one comment's Markdown cannot run into the next. d is nil while the
// load has not arrived. Bodies are raw Markdown (the renderer sanitizes
// them); tracker text in headings is escaped.
func Sections(id string, d *Detail) []detail.Section {
	body := id + "/body"
	switch {
	case d == nil:
		return []detail.Section{{RecordID: body, Content: Loading}}
	case d.Issue == nil:
		return []detail.Section{{RecordID: body, Content: issueFailed + Escape(recovery.Describe(d.Err)) + "*"}}
	}
	rev := d.Issue.Revision
	text := d.Issue.Body
	if strings.TrimSpace(text) == "" {
		text = NoBody
	}
	secs := []detail.Section{{RecordID: body, Revision: rev, Content: text}, {RecordID: id + "/comments", Revision: rev, Content: heading(d)}}
	for i, c := range d.Comments {
		secs = append(secs, detail.Section{RecordID: c.ID, Revision: c.Revision, Content: comment(c, i > 0)})
	}
	return secs
}

// heading is the comments heading: a count only for a consistent read.
func heading(d *Detail) string {
	notes := []string{"---", "## Comments"}
	switch {
	case !d.Consistent:
		notes = append(notes, PagedNote)
	case len(d.Comments) == 0:
		notes = append(notes, NoComments)
	default:
		notes[1] += " (" + strconv.Itoa(len(d.Comments)) + ")"
	}
	if d.Err != nil {
		notes = append(notes, pagesFailed+Escape(recovery.Describe(d.Err))+"*")
	}
	return strings.Join(notes, "\n\n")
}

// comment is a comment's author and time as a heading, so it stands apart
// from body text, then its body. A rule separates it from an earlier
// comment.
func comment(c protocol.Comment, rule bool) string {
	head := "### " + Escape(c.Author) + " · " + Escape(c.CreatedAt)
	if c.Revision > 1 {
		head += " · edited " + Escape(c.UpdatedAt)
	}
	if rule {
		head = "---\n\n" + head
	}
	return head + "\n\n" + c.Body
}

// Escape backslash-escapes the ASCII punctuation that forms Markdown
// syntax, so tracker text renders as literal text, and keeps it on one
// line. Only escapes the renderer removes are used: others would show their
// backslash.
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r':
			b.WriteRune(' ')
		case r < 0x80 && strings.ContainsRune("\\`*_{}[]<>()#+-.!|", r):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
