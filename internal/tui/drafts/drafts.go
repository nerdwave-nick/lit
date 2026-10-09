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
package drafts

import (
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// SchemaVersion is the sidecar's schema version.
const SchemaVersion = 1

// Kind is what a draft edits.
type Kind string

// IssueBody is the body of an existing issue. Later kinds (comments,
// specifications, milestone descriptions) add their own.
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
	// RecordID is the edited record; IssueID is the issue that owns it (the
	// same for an issue body).
	RecordID string `json:"record_id"`
	IssueID  string `json:"issue_id"`
	// Title is the record's title when the draft was started or reloaded.
	Title string `json:"title"`
	// File is the draft file's base name in the project's drafts directory.
	File string `json:"file"`
	// Revision is the record revision the draft applies at; Base is the
	// record's content at that revision.
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

func (d Draft) key() string { return string(d.Kind) + "-" + d.RecordID }

// Errors, typed like the client's: callers branch on protocol codes.
var (
	errExists = func(what string) error {
		return protocol.E(409, "draft_exists", what+" already has a draft; resume, diff or discard it")
	}
	errMissing = func(path string) error {
		return protocol.E(404, "draft_missing", "the draft file "+path+" is missing")
	}
)
