package cli

import (
	"fmt"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func (a *App) projectRef() string {
	if a.Args.Has("project") {
		return a.Args.One("project")
	}
	if a.Client.ProjectID != nil {
		return *a.Client.ProjectID
	}
	return ""
}

// resolveIssueRefs turns user-facing selectors into the stable, project-local
// UUIDs accepted by milestone membership operations. One snapshot keeps a
// repeated set of references coherent and preserves the caller's project scope.
func (a *App) resolveIssueRefs(refs []string, project string) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if project == "" {
		return nil, fmt.Errorf("milestone membership requires project selection")
	}
	targets := make([]map[string]string, 0, len(refs))
	for _, ref := range refs {
		targets = append(targets, map[string]string{"type": "issues", "selector": ref, "project": project})
	}
	var snapshot Result
	if err := a.Call("POST", "/v1/snapshots", map[string]any{"schema_version": 1, "targets": targets}, nil, &snapshot, false); err != nil {
		return nil, err
	}
	if len(snapshot.Items) > len(refs) {
		return nil, protocol.E(500, "invalid_response", "issue reference snapshot returned an unexpected number of members")
	}
	ids := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, value := range snapshot.Items {
		issue, ok := value.(map[string]any)
		if !ok {
			return nil, protocol.E(500, "invalid_response", "issue reference snapshot returned an invalid member")
		}
		id, ok := issue["id"].(string)
		if !ok || !protocol.ValidUUID(id) {
			return nil, protocol.E(500, "invalid_response", "issue reference snapshot returned an invalid member ID")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (a *App) resolveMilestoneProject(selector, project string) (string, error) {
	target := map[string]string{"type": "milestones", "selector": selector}
	if project != "" {
		target["project"] = project
	}
	var snapshot Result
	if err := a.Call("POST", "/v1/snapshots", map[string]any{"schema_version": 1, "targets": []map[string]string{target}}, nil, &snapshot, false); err != nil {
		return "", err
	}
	if len(snapshot.Items) != 1 {
		return "", protocol.E(500, "invalid_response", "milestone target snapshot returned an unexpected number of records")
	}
	milestone, ok := snapshot.Items[0].(map[string]any)
	if !ok {
		return "", protocol.E(500, "invalid_response", "milestone target snapshot returned an invalid record")
	}
	projectID, ok := milestone["project_id"].(string)
	if !ok || !protocol.ValidUUID(projectID) {
		return "", protocol.E(500, "invalid_response", "milestone target snapshot returned an invalid project ID")
	}
	return projectID, nil
}
