package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// APIError is a failed request: lit's error envelope, or a transport failure
// with code "timeout" or "transport".
type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

// errorCode is err's lit error code, or "harness" for a harness error.
func errorCode(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return "harness"
}

// Client is one connected lit client speaking the public HTTP API directly,
// as lit does: writes are previewed, then committed. It measures each HTTP
// request from sending it to reading the whole response; no client-side
// journaling is timed. A Client is not safe for concurrent use.
type Client struct {
	Endpoint, ClientID string
	HTTP               *http.Client
	// Rec receives the latency of every operation (and of the requests of
	// two-request operations as op/preview, op/commit, op/snapshot,
	// op/operation); nil records nothing.
	Rec *Recorder
}

type envelope struct {
	Data       json.RawMessage   `json:"data"`
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
	Error      *protocol.Error   `json:"error"`
	ServerTime string            `json:"server_time"`
}

// Connect registers a new agent client.
func Connect(endpoint, name string, hc *http.Client) (*Client, error) {
	c := &Client{Endpoint: endpoint, HTTP: hc}
	var out struct {
		Client protocol.Client `json:"client"`
	}
	env, err := c.request("", "POST", "/v1/connect", map[string]any{"actor": map[string]string{"name": name, "kind": "agent"}})
	if err == nil {
		err = json.Unmarshal(env.Data, &out)
	}
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", name, err)
	}
	c.ClientID = out.Client.ClientID
	return c, nil
}

// request sends one request and returns its decoded envelope. A non-empty
// step records its latency.
func (c *Client) request(step, method, path string, body any) (envelope, error) {
	var env envelope
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return env, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Endpoint+path, reader)
	if err != nil {
		return env, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.ClientID != "" {
		req.Header.Set("X-Lit-Client-ID", c.ClientID)
	}
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	var raw []byte
	if err == nil {
		raw, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	elapsed := time.Since(start)
	if err != nil {
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
			return env, &APIError{Code: "timeout", Message: err.Error()}
		}
		return env, &APIError{Code: "transport", Message: err.Error()}
	}
	if err = json.Unmarshal(raw, &env); err != nil {
		return env, &APIError{Status: resp.StatusCode, Code: "invalid_response", Message: string(raw)}
	}
	if env.Error != nil {
		return env, &APIError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
	}
	if resp.StatusCode/100 != 2 {
		return env, &APIError{Status: resp.StatusCode, Code: "invalid_response", Message: string(raw)}
	}
	if step != "" && c.Rec != nil {
		c.Rec.Add(step, elapsed)
	}
	return env, nil
}

// op times fn as operation name and records its latency or failure code.
func (c *Client) op(name string, fn func() error) error {
	start := time.Now()
	err := fn()
	if c.Rec != nil {
		if err != nil {
			c.Rec.Fail(name, errorCode(err), err.Error())
		} else {
			c.Rec.Add(name, time.Since(start))
		}
	}
	return err
}

// single runs a one-request operation.
func (c *Client) single(name, method, path string, body any) (envelope, error) {
	var env envelope
	err := c.op(name, func() (err error) {
		env, err = c.request("", method, path, body)
		return err
	})
	return env, err
}

// Meta reads GET /v1/meta.
func (c *Client) Meta() error {
	_, err := c.single("meta", "GET", "/v1/meta", nil)
	return err
}

// GetIssue reads one issue as lit issues get does.
func (c *Client) GetIssue(id string) error {
	_, err := c.single("issue.get", "POST", "/v1/snapshots", map[string]any{"schema_version": 1, "targets": []map[string]string{{"type": "issues", "selector": id}}})
	return err
}

// ListIssues reads one 100-item page of the bodiless issue list across all
// projects and returns the next cursor.
func (c *Client) ListIssues(cursor string) (string, error) {
	q := map[string]any{"all_projects": true, "limit": 100}
	if cursor != "" {
		q["cursor"] = cursor
	}
	env, err := c.single("issues.list", "POST", "/v1/issue-summaries", map[string]any{"schema_version": 1, "query": q})
	if err != nil || env.NextCursor == nil {
		return "", err
	}
	return *env.NextCursor, nil
}

// ProjectSnapshot reads every issue of a project with bodies in one read.
func (c *Client) ProjectSnapshot(project string) (int, error) {
	env, err := c.single("project.snapshot", "POST", "/v1/snapshots", map[string]any{"schema_version": 1, "query": map[string]any{"type": "issues", "project_id": project, "all": true}})
	return len(env.Items), err
}

// Grep runs lit grep's literal search over all projects as operation name.
func (c *Client) Grep(name, query string) (int, error) {
	env, err := c.single(name, "POST", "/v1/search", protocol.SearchRequest{Scope: protocol.SearchScope{AllProjects: true}, Query: query})
	return len(env.Items), err
}

// History reads an issue's whole history in one read.
func (c *Client) History(id string) (int, error) {
	env, err := c.single("history.200", "GET", "/v1/issues/"+id+"/history?all=true", nil)
	return len(env.Items), err
}
