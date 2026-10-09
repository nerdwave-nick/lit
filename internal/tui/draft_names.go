package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

// Names of what a draft edits, for notes and panels. An issue body keeps
// the original wording ("the draft of “T”"); comments name their issue.

// nameOf names the record a draft of kind k edits: “T” for an issue body,
// the (new) comment on “T” for comments.
func nameOf(k drafts.Kind, title string) string {
	switch k {
	case drafts.IssueBody:
		return titled(title)
	case drafts.CommentNew:
		return "the new comment on " + titled(title)
	case drafts.CommentBody:
		return "the comment on " + titled(title)
	case drafts.IssueNew:
		return "the new issue " + titled(title)
	}
	return "the " + strings.ReplaceAll(string(k), "_", " ") + " of " + titled(title)
}

// draftOf names a draft of kind k: the draft of “T”, the new comment on
// “T”, the draft of the comment on “T”.
func draftOf(k drafts.Kind, title string) string {
	if k == drafts.CommentNew || k == drafts.IssueNew {
		return nameOf(k, title)
	}
	if k == drafts.IssueBody {
		return "the draft of " + titled(title)
	}
	return "the draft of " + nameOf(k, title)
}

// noun is what a draft of kind k replaces: the body or the comment.
func noun(k drafts.Kind) string {
	switch k {
	case drafts.CommentNew, drafts.CommentBody:
		return "comment"
	case drafts.IssueBody:
		return "body"
	case drafts.IssueNew:
		return "issue"
	}
	return "content"
}

// posted is what applying a new record's draft did: a comment is posted,
// an issue created.
func posted(k drafts.Kind) string {
	if k == drafts.IssueNew {
		return "created"
	}
	return "posted"
}

// upper starts s with a capital letter.
func upper(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// keyFor is the key that resumes, applies or discards a draft of kind k:
// edit for a body, new_comment for a new comment, edit_comment (then the
// comment) for a comment.
func (m Model) keyFor(k drafts.Kind) string {
	switch k {
	case drafts.CommentNew:
		return firstKey(m, keys.Tree, keys.NewComment)
	case drafts.CommentBody:
		return firstKey(m, keys.Tree, keys.EditComment)
	case drafts.IssueNew:
		return firstKey(m, keys.Global, keys.Drafts) + " (the Drafts view)"
	}
	return m.editKey()
}

// resumeHint says how to come back to a kept draft of kind k: its key on
// the issue, or for a new issue (which has no issue yet) the Drafts view.
func (m Model) resumeHint(k drafts.Kind) string {
	if k == drafts.IssueNew {
		return m.keyFor(k) + " resumes, creates or discards it."
	}
	return m.keyFor(k) + " on the issue resumes, applies or discards it."
}

// placeOf says where a new issue's draft creates it.
func (m Model) placeOf(d drafts.Draft) string {
	if d.ParentID == "" {
		return "It creates a top-level issue."
	}
	return "It creates a child of " + m.issueName(d.ParentID) + "."
}

// text is a choice's text for the panel's draft: a diff compares with the
// current comment rather than the body.
func (c choice) text(d drafts.Draft) string {
	if c.kind == chDiff && d.Kind != drafts.IssueBody {
		return "Diff against the current " + noun(d.Kind)
	}
	return c.label
}
