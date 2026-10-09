// Package drafts is lit-tui's store of TUI drafts: local files holding exactly
// one record's editable Markdown content, each with a TUI-owned sidecar that
// records its identity.
//
// A TUI draft is not a client draft (stashed with lit-server) and not an
// editing window: nothing about it is known to the service.
//
// Layout, per project, under the client state directory:
//
//	<state>/tui/drafts/<project-id>/<title-slug>--<id8>.md    exactly the content
//	<state>/tui/drafts/<project-id>/.lit/<kind>-<record>.json  the sidecar
//
// The sidecar names the draft file, so listing and discarding never depend on
// the names of other files in the directory: editor swap and backup files
// there are ignored when listing, and a draft's own leftovers are removed
// when it is discarded. One record has at most one draft of a kind, and an
// existing draft is never overwritten.
//
// Kinds declare whether their record exists yet and whether an issue owns
// it (kinds.go): a new comment's draft is keyed by a generated draft ID, and
// a project specification's draft has no owning issue.
package drafts

import (
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// SchemaVersion is the sidecar's schema version.
const SchemaVersion = 1

// Kind is what a draft edits.
type Kind string

// IssueBody is the body of an existing issue; editing it claims the issue.
// The other kinds, and what each declares, are in kinds.go.
const IssueBody Kind = "issue_body"

// ClaimState is the edit-claim state recorded for a draft.
type ClaimState string

const (
	// ClaimNone: no claim belongs to the draft (claim-exempt kinds, or none
	// held yet in this session).
	ClaimNone ClaimState = "none"
	// ClaimEdit: the draft's edit acquired the claim; saving or cancelling
	// releases it.
	ClaimEdit ClaimState = "edit"
	// ClaimExplicit: the user already held the claim when the edit started;
	// it is kept after saving or cancelling.
	ClaimExplicit ClaimState = "explicit"
)

// Draft is a draft's sidecar: its identity, the recorded revision and the
// content at that revision (Base), so a diff against the recorded revision
// needs no history read.
type Draft struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          Kind   `json:"kind"`
	ServiceID     string `json:"service_id"`
	ProjectID     string `json:"project_id"`
	// RecordID is the edited record, or for a record that does not exist
	// yet (Kind.New) a generated draft ID (NewID). IssueID is the issue that
	// owns it (the same for an issue body, the commented issue for comment
	// kinds); it is empty for kinds without an owner (Kind.Owned).
	RecordID string `json:"record_id"`
	IssueID  string `json:"issue_id"`
	// ParentID is the issue a new issue (issue_new) is created under; empty
	// for a top-level issue and for every other kind. It is written only
	// when set, so drafts without one keep the original sidecar fields.
	ParentID string `json:"parent_id,omitempty"`
	// Title is the record's title when the draft was started or reloaded.
	Title string `json:"title"`
	// File is the draft file's base name in the project's drafts directory.
	File string `json:"file"`
	// Revision is the record revision the draft applies at; Base is the
	// record's content at that revision. A new record has neither (0, "").
	Revision  int64      `json:"revision"`
	Base      string     `json:"base"`
	Claim     ClaimState `json:"claim"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`

	dir string // the project's drafts directory
}

// Path is the draft file's path.
func (d Draft) Path() string { return join(d.dir, d.File) }

// Dir is the project's drafts directory holding the draft file.
func (d Draft) Dir() string { return d.dir }

// Created is when the draft was started.
func (d Draft) Created() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, d.CreatedAt)
	return t
}

// Key identifies the draft among all of a project's drafts: its kind and
// record. One record has at most one draft of a kind.
func (d Draft) Key() string { return string(d.Kind) + "-" + d.RecordID }

// Errors, typed like the client's: callers branch on protocol codes.
var (
	errExists = func(what string) error {
		return protocol.E(409, "draft_exists", what+" already has a draft; resume, diff or discard it")
	}
	errMissing = func(path string) error {
		return protocol.E(404, "draft_missing", "the draft file "+path+" is missing")
	}
)
