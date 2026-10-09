package drafts

import "github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"

// The draft kinds. Each declares, in kinds below, whether its record exists
// yet, whether an issue owns it (Draft.IssueID) and so whether an edit of it
// claims that issue (claim-on-edit). Later tickets plug their kind in by
// adding its row; the store itself does not change.
const (
	// CommentNew is a comment that does not exist yet, on the owning issue.
	// Its RecordID is a generated draft ID (NewID), which becomes the
	// comment's ID when it is applied. New comments are claim-exempt
	// appends: their drafts take no claim.
	CommentNew Kind = "comment_new"
	// CommentBody is an existing comment's body; editing it claims the
	// owning issue.
	CommentBody Kind = "comment_body"

	// Reserved for later tickets (rows are declared below so their drafts
	// already validate):

	// IssueNew is the body of an issue that does not exist yet. Its RecordID
	// is a generated draft ID; it has no owning issue and takes no claim.
	IssueNew Kind = "issue_new"
	// MilestoneDescription is a milestone's description. Milestones have no
	// claims: no owning issue, and drafts are optimistic and revision-fenced.
	MilestoneDescription Kind = "milestone_description"
	// ProjectSpec is a project's shared specification (its description).
	// Projects have no claims either.
	ProjectSpec Kind = "project_spec"
)

// kindInfo is what a kind declares.
type kindInfo struct {
	// fresh: the record does not exist until the draft is applied; the
	// draft's RecordID is a generated draft ID and its Revision is 0.
	fresh bool
	// unowned: no issue owns the record; Draft.IssueID is empty.
	unowned bool
	// prefix starts the title part of the draft's file name.
	prefix string
}

var kinds = map[Kind]kindInfo{
	IssueBody:            {},
	CommentNew:           {fresh: true, prefix: "comment"},
	CommentBody:          {prefix: "comment"},
	IssueNew:             {fresh: true, unowned: true},
	MilestoneDescription: {unowned: true},
	ProjectSpec:          {unowned: true},
}

// New reports whether k's record does not exist until the draft is
// applied (comment_new, issue_new). Such a draft's RecordID is a generated
// draft ID, it records no revision (0) and its base content is empty.
func (k Kind) New() bool { return kinds[k].fresh }

// Owned reports whether an issue owns k's records, so its drafts carry that
// issue's UUID as IssueID. Kinds without an owner (issue_new,
// milestone_description, project_spec) have an empty IssueID. A kind no
// row declares is owned, as every kind was before kinds declared it.
func (k Kind) Owned() bool { return !kinds[k].unowned }

// Claims reports whether editing k claims the owning issue
// (claim-on-edit): owned kinds whose record exists. New comments are
// claim-exempt; projects and milestones have no claims.
func (k Kind) Claims() bool { return k.Owned() && !k.New() }

// NewID is a generated draft ID for a draft of a record that does not exist
// yet (Kind.New): its RecordID until it is applied.
func NewID() string { return protocol.UUID() }
