package workflowsession

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// QueryTimed is Query that also returns the service time the answer was read
// at, zero when the answer carries none. Like Query it takes no lock and never
// changes the association.
func (a Adapter) QueryTimed(ctx context.Context, args ...string) ([]map[string]json.RawMessage, time.Time, error) {
	r, e := a.Read()
	if e != nil {
		return nil, time.Time{}, e
	}
	if r.Status != "ready" {
		return nil, time.Time{}, fmt.Errorf("connection is pending; reconcile before continuing")
	}
	binary, e := a.binary()
	if e != nil {
		return nil, time.Time{}, e
	}
	out, stderr, code, e := lit{binary, a.WorkDir}.invoke(ctx, r, args)
	if e != nil {
		return nil, time.Time{}, e
	}
	if code != 0 {
		return nil, time.Time{}, fmt.Errorf("lit failed; retained association, inspect CLI diagnostics/pending state: %s %s", stderr, out)
	}
	var answer struct {
		Items      []map[string]json.RawMessage `json:"items"`
		ServerTime string                       `json:"server_time"`
	}
	if e = json.Unmarshal(out, &answer); e != nil {
		return nil, time.Time{}, e
	}
	at, _ := time.Parse(time.RFC3339Nano, answer.ServerTime)
	return answer.Items, at, nil
}
