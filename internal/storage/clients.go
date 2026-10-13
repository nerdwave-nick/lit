package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Client reads the client record id; ok is false when none exists. Records
// carry schema version 1, which is not stored.
func (q queries) Client(ctx context.Context, id string) (c protocol.Client, ok bool, err error) {
	err = q.queryRow(ctx, []any{&c.ClientID, &c.StateRevision, &c.Status, &c.Actor.Name, &c.Actor.Kind, &c.ProjectID, &c.Runtime.Vendor, &c.Runtime.SessionID, &c.OutputFormat},
		"SELECT client_id, state_revision, status, actor_name, actor_kind, project_id, runtime_vendor, runtime_session_id, output_format FROM clients WHERE client_id = ?", id)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.Client{}, false, nil
	}
	if err != nil {
		return protocol.Client{}, false, err
	}
	c.SchemaVersion = 1
	return c, true, nil
}

// PutClient inserts or replaces the client record c. The service validates
// clients first; the schema's checks are the backstop.
func (t *WriteTx) PutClient(ctx context.Context, c protocol.Client) error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("client %s: unsupported schema_version %d", c.ClientID, c.SchemaVersion)
	}
	_, e := t.exec(ctx, `INSERT INTO clients (client_id, state_revision, status, actor_name, actor_kind, project_id, runtime_vendor, runtime_session_id, output_format)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (client_id) DO UPDATE SET state_revision = excluded.state_revision, status = excluded.status,
	actor_name = excluded.actor_name, actor_kind = excluded.actor_kind, project_id = excluded.project_id,
	runtime_vendor = excluded.runtime_vendor, runtime_session_id = excluded.runtime_session_id, output_format = excluded.output_format`,
		c.ClientID, c.StateRevision, c.Status, c.Actor.Name, c.Actor.Kind, c.ProjectID, c.Runtime.Vendor, c.Runtime.SessionID, c.OutputFormat)
	return e
}

// DisconnectClient deletes every claim c owns, live or expired, and writes c,
// whose status must already be disconnected. It is all or nothing on its own:
// a failure undoes its earlier statements even if the caller still commits.
// A failure later in the same transaction undoes it with the rollback.
func (t *WriteTx) DisconnectClient(ctx context.Context, c protocol.Client) (err error) {
	if c.Status != "disconnected" {
		return fmt.Errorf("client %s: disconnect writes a disconnected client, not %q", c.ClientID, c.Status)
	}
	if _, err = t.exec(ctx, "SAVEPOINT disconnect_client"); err != nil {
		return err
	}
	defer func() {
		// Undo even when ctx has ended, so no partial disconnect can commit.
		cleanup := context.WithoutCancel(ctx)
		if err != nil {
			_, _ = t.exec(cleanup, "ROLLBACK TO disconnect_client")
		}
		if _, e := t.exec(cleanup, "RELEASE disconnect_client"); err == nil {
			err = e
		}
	}()
	if _, err = t.exec(ctx, "DELETE FROM claims WHERE owner_client_id = ?", c.ClientID); err != nil {
		return err
	}
	return t.PutClient(ctx, c)
}
