package workflowsession

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
)

// ShowNotFound is the exit status of show for a runtime without an
// association; 1 remains every other failure, such as a damaged record.
const ShowNotFound = 3

// notFound is the JSON error object show prints for a runtime without an
// association, in the shape of the CLI's error contract.
type notFound struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

// Show prints the runtime's stored association exactly as the lifecycle
// commands print it. It takes no lock, runs no lit command, contacts no
// service and writes no state, so it answers while another adapter call holds
// the runtime's lock. A runtime without an association prints an
// association_not_found error object and returns ShowNotFound.
func (a Adapter) Show(out io.Writer) (int, error) {
	r, e := a.Read()
	if errors.Is(e, fs.ErrNotExist) {
		var n notFound
		n.Error.Code = "association_not_found"
		n.Error.Message = "no association for this runtime; create one with workflow-session fresh or subagent"
		n.Error.Details = map[string]string{"host": a.Host, "runtime_id": a.RuntimeID}
		return ShowNotFound, json.NewEncoder(out).Encode(n)
	}
	if e != nil {
		return 1, e
	}
	return 0, json.NewEncoder(out).Encode(r)
}
