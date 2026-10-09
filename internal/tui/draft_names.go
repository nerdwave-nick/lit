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
	case drafts.ProjectSpec:
		return "the specification of " + titled(title)
	}
	return "the " + strings.ReplaceAll(string(k), "_", " ") + " of " + titled(title)
}

// draftOf names a draft of kind k: the draft of “T”, the new comment on
// “T”, the draft of the comment on “T”.
func draftOf(k drafts.Kind, title string) string {
	if k == drafts.CommentNew {
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
	case drafts.ProjectSpec:
		return "specification"
	}
	return "content"
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
	case drafts.ProjectSpec:
		return firstKey(m, keys.Tree, keys.EditSpec)
	}
	return m.editKey()
}

// text is a choice's text for the panel's draft: a diff compares with the
// current comment rather than the body.
func (c choice) text(d drafts.Draft) string {
	if c.kind == chDiff && d.Kind != drafts.IssueBody {
		return "Diff against the current " + noun(d.Kind)
	}
	return c.label
}
