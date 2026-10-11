-- lit storage schema version 1.
--
-- The service validates every rule in Go first; these constraints are the
-- backstop. Timestamps of records and transactions are kept as their exact
-- RFC3339 text. Claim times are Unix nanoseconds so that expiry and ordering
-- compare numerically (claims format them with RFC3339Nano, whose width varies).
-- Record tables keep their rowid so search tables can index them as external
-- content. Title keys and folded text are computed in Go (NFC, full case fold,
-- NFC); an issue's or milestone's title key is also its folded title.

CREATE TABLE service (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	service_id TEXT NOT NULL CHECK (length(service_id) = 36),
	schema_version INTEGER NOT NULL CHECK (schema_version >= 1)
) STRICT;

CREATE TABLE projects (
	id TEXT PRIMARY KEY CHECK (length(id) = 36),
	revision INTEGER NOT NULL CHECK (revision >= 1),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	title TEXT NOT NULL CHECK (title <> ''),
	description TEXT NOT NULL,
	folded_description TEXT NOT NULL
) STRICT;
-- Project titles are ASCII, so SQLite's ASCII lower() is their case fold.
CREATE UNIQUE INDEX projects_title ON projects (lower(title));

CREATE TABLE project_repositories (
	project_id TEXT NOT NULL REFERENCES projects (id),
	repository_ref TEXT NOT NULL CHECK (repository_ref <> ''),
	PRIMARY KEY (project_id, repository_ref)
) STRICT, WITHOUT ROWID;

CREATE TABLE issues (
	id TEXT PRIMARY KEY CHECK (length(id) = 36),
	project_id TEXT NOT NULL REFERENCES projects (id),
	revision INTEGER NOT NULL CHECK (revision >= 1),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	title TEXT NOT NULL CHECK (title <> ''),
	title_key TEXT NOT NULL CHECK (title_key <> ''),
	body TEXT NOT NULL,
	folded_body TEXT NOT NULL,
	state TEXT NOT NULL CHECK (state IN ('open', 'closed')),
	assignee TEXT,
	parent_id TEXT CHECK (parent_id <> id),
	UNIQUE (project_id, title_key),
	UNIQUE (id, project_id),
	FOREIGN KEY (parent_id, project_id) REFERENCES issues (id, project_id)
) STRICT;
CREATE INDEX issues_project ON issues (project_id, id);
-- Keyset pages of a project's issues in the default sort orders.
CREATE INDEX issues_project_created ON issues (project_id, created_at, id);
CREATE INDEX issues_project_updated ON issues (project_id, updated_at, id);
CREATE INDEX issues_parent ON issues (parent_id, id) WHERE parent_id IS NOT NULL;

CREATE TABLE issue_labels (
	issue_id TEXT NOT NULL REFERENCES issues (id),
	label TEXT NOT NULL CHECK (label <> ''),
	PRIMARY KEY (issue_id, label)
) STRICT, WITHOUT ROWID;
CREATE INDEX issue_labels_label ON issue_labels (label, issue_id);

-- blocks: a blocks b, stored once in that direction (b's blocked_by is the
-- reverse lookup). related: symmetric, stored once with a < b. Links may
-- cross projects.
CREATE TABLE issue_links (
	kind TEXT NOT NULL CHECK (kind IN ('blocks', 'related')),
	a TEXT NOT NULL REFERENCES issues (id),
	b TEXT NOT NULL REFERENCES issues (id),
	PRIMARY KEY (kind, a, b),
	CHECK (a <> b),
	CHECK (kind <> 'related' OR a < b)
) STRICT, WITHOUT ROWID;
CREATE INDEX issue_links_b ON issue_links (b, kind, a);

CREATE TABLE comments (
	id TEXT PRIMARY KEY CHECK (length(id) = 36),
	issue_id TEXT NOT NULL REFERENCES issues (id),
	revision INTEGER NOT NULL CHECK (revision >= 1),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	author TEXT NOT NULL,
	body TEXT NOT NULL,
	folded_body TEXT NOT NULL
) STRICT;
CREATE INDEX comments_issue ON comments (issue_id, id);

CREATE TABLE milestones (
	id TEXT PRIMARY KEY CHECK (length(id) = 36),
	project_id TEXT NOT NULL REFERENCES projects (id),
	revision INTEGER NOT NULL CHECK (revision >= 1),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	title TEXT NOT NULL CHECK (title <> ''),
	title_key TEXT NOT NULL CHECK (title_key <> ''),
	body TEXT NOT NULL,
	folded_body TEXT NOT NULL,
	UNIQUE (project_id, title_key),
	UNIQUE (id, project_id)
) STRICT;
CREATE INDEX milestones_project ON milestones (project_id, id);

-- Members belong to the milestone's project: both keys carry project_id.
CREATE TABLE milestone_members (
	milestone_id TEXT NOT NULL,
	issue_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	PRIMARY KEY (milestone_id, issue_id),
	FOREIGN KEY (milestone_id, project_id) REFERENCES milestones (id, project_id),
	FOREIGN KEY (issue_id, project_id) REFERENCES issues (id, project_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX milestone_members_issue ON milestone_members (issue_id, milestone_id);

CREATE TABLE clients (
	client_id TEXT PRIMARY KEY CHECK (length(client_id) = 36),
	state_revision INTEGER NOT NULL CHECK (state_revision >= 1),
	status TEXT NOT NULL CHECK (status IN ('connected', 'disconnected')),
	actor_name TEXT NOT NULL CHECK (actor_name <> ''),
	actor_kind TEXT NOT NULL CHECK (actor_kind IN ('human', 'agent')),
	project_id TEXT,
	runtime_vendor TEXT NOT NULL,
	runtime_session_id TEXT NOT NULL,
	output_format TEXT
) STRICT;

-- One live or expired claim per issue; release deletes the row.
CREATE TABLE claims (
	issue_id TEXT PRIMARY KEY REFERENCES issues (id),
	owner_client_id TEXT NOT NULL REFERENCES clients (client_id),
	token TEXT NOT NULL CHECK (length(token) = 36),
	acquired_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	CHECK (expires_at > acquired_at)
) STRICT;
CREATE INDEX claims_owner ON claims (owner_client_id, acquired_at);
CREATE INDEX claims_expiry ON claims (expires_at);

-- One row per committed durable request, in commit order (seq). The canonical
-- intent and the results are stored once, as JSON.
CREATE TABLE transactions (
	seq INTEGER PRIMARY KEY,
	request_hash TEXT NOT NULL UNIQUE CHECK (request_hash <> ''),
	client_id TEXT NOT NULL,
	actor_name TEXT NOT NULL,
	actor_kind TEXT NOT NULL,
	timestamp TEXT NOT NULL,
	intent TEXT NOT NULL CHECK (json_valid(intent)),
	results TEXT NOT NULL CHECK (json_valid(results))
) STRICT;

-- One row per changed record per transaction. differences holds the non-body
-- field changes ({"field": {"before": ..., "after": ...}}); body_diff is the
-- forward unified diff from the previous body, NULL when the body is unchanged.
CREATE TABLE history (
	owner_type TEXT NOT NULL CHECK (owner_type IN ('projects', 'issues', 'comments', 'milestones')),
	owner_id TEXT NOT NULL,
	revision INTEGER NOT NULL CHECK (revision >= 1),
	request_hash TEXT NOT NULL REFERENCES transactions (request_hash),
	differences TEXT NOT NULL CHECK (json_valid(differences)),
	body_diff TEXT,
	PRIMARY KEY (owner_type, owner_id, revision)
) STRICT, WITHOUT ROWID;
CREATE INDEX history_request ON history (request_hash);
