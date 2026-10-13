package protocol

// ProjectContext is the answer to GET /v1/project-context: the identity of the
// service that answered and the one project it resolved. Like Completion it is
// an explicit projection (no description, repositories, issues or revision), so
// a future record field cannot reach this session-free read by accident.
type ProjectContext struct {
	ServiceID string          `json:"service_id"`
	APIMajor  int             `json:"api_major"`
	Project   ProjectIdentity `json:"project"`
}

// ProjectIdentity is a project's stable ID with its current title. The ID is
// the identity; the title is display text that a rename changes.
type ProjectIdentity struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
