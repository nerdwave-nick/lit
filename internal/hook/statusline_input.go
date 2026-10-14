package hook

import (
	"encoding/json"
	"math"
)

// statusInput is Claude Code's status line input reduced to the fields the
// built-in line shows. Anything missing, null or of an unexpected type is left
// empty: the host omits fields until they exist (no effort for a model without
// the parameter, no context percentage before the first response).
type statusInput struct {
	Model   string // the model's display name, else its ID
	Effort  string
	Dir     string // the workspace's current directory, else cwd
	Percent int    // context used, whole percent
}

// parseStatusInput reads the status line JSON; a field of the wrong type does
// not stop the others from being read.
func parseStatusInput(body []byte) statusInput {
	var raw struct {
		Cwd   string `json:"cwd"`
		Model struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"model"`
		Effort struct {
			Level string `json:"level"`
		} `json:"effort"`
		Workspace struct {
			CurrentDir string `json:"current_dir"`
		} `json:"workspace"`
		ContextWindow struct {
			UsedPercentage *float64 `json:"used_percentage"`
		} `json:"context_window"`
	}
	_ = json.Unmarshal(body, &raw)
	in := statusInput{Model: raw.Model.DisplayName, Effort: raw.Effort.Level, Dir: raw.Workspace.CurrentDir}
	if in.Model == "" {
		in.Model = raw.Model.ID
	}
	if in.Dir == "" {
		in.Dir = raw.Cwd
	}
	if p := raw.ContextWindow.UsedPercentage; p != nil && !math.IsNaN(*p) && *p > 0 {
		in.Percent = int(math.Floor(math.Min(*p, 1000)))
	}
	return in
}
