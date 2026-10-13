package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func (s *Server) client(id string) (protocol.Client, error) {
	if !protocol.ValidUUID(id) {
		return protocol.Client{}, protocol.E(404, "unknown_client", "unknown client identity")
	}
	c, ok, e := s.q().Client(s.ctx(), id)
	if e != nil {
		return c, e
	}
	if !ok {
		return c, protocol.E(404, "unknown_client", "unknown client identity")
	}
	return c, nil
}
func validateClient(c protocol.Client) error {
	if strings.TrimSpace(c.Actor.Name) == "" || (c.Actor.Kind != "human" && c.Actor.Kind != "agent") {
		return fmt.Errorf("actor requires a name and human or agent kind")
	}
	if c.OutputFormat != nil && *c.OutputFormat != "json" && *c.OutputFormat != "cli" && *c.OutputFormat != "markdown" && *c.OutputFormat != "human" {
		return fmt.Errorf("output_format must be cli, markdown or json (human is a legacy alias)")
	}
	if c.ProjectID != nil && !protocol.ValidUUID(*c.ProjectID) {
		return fmt.Errorf("invalid project ID")
	}
	return nil
}
func precondition(r *http.Request, c protocol.Client) error {
	v := r.Header.Get("If-Match")
	if v == "" {
		return protocol.E(428, "precondition_required", "If-Match client revision required")
	}
	if v != fmt.Sprintf("\"client:%d\"", c.StateRevision) {
		return protocol.E(409, "client_state_conflict", "client state revision changed")
	}
	return nil
}
func (s *Server) disconnect(w http.ResponseWriter, r *http.Request, c protocol.Client) error {
	var input struct{}
	if e := s.Body(r, &input); e != nil {
		return e
	}
	if e := precondition(r, c); e != nil {
		return e
	}
	if c.Status == "disconnected" {
		s.write(w, 200, map[string]any{"outcome": "already_disconnected", "client": c})
		return nil
	}
	c.Status = "disconnected"
	c.StateRevision++
	// Disconnect clears every claim the client owns, live or expired, with it.
	return s.commitResponse(w, 200, map[string]any{"outcome": "applied", "client": c}, changes{client: &c, disconnect: true})
}
func (s *Server) clientRoute(w http.ResponseWriter, r *http.Request, c protocol.Client) error {
	if r.URL.Path != "/v1/clients/"+c.ClientID {
		return protocol.E(403, "wrong_client", "client may access only its own state")
	}
	switch r.Method {
	case "GET":
		s.write(w, 200, c)
		return nil
	case "PATCH":
		if e := precondition(r, c); e != nil {
			return e
		}
		var p map[string]json.RawMessage
		if e := s.Body(r, &p); e != nil {
			return e
		}
		old, _ := json.Marshal(c)
		if e := s.patchClient(&c, p); e != nil {
			return e
		}
		next, _ := json.Marshal(c)
		var cs changes
		if string(old) != string(next) {
			c.StateRevision++
			cs.client = &c
		}
		return s.commitResponse(w, 200, c, cs)
	}
	return protocol.E(404, "not_found", "unknown route")
}
func patch(c *protocol.Client, p map[string]json.RawMessage) error {
	for k, b := range p {
		switch k {
		case "actor":
			var fields map[string]json.RawMessage
			if e := protocol.Decode(b, &fields); e != nil || fields == nil {
				return protocol.E(422, "validation_failed", "invalid actor")
			}
			for key, v := range fields {
				switch key {
				case "name":
					if e := json.Unmarshal(v, &c.Actor.Name); e != nil || string(v) == "null" {
						return protocol.E(422, "validation_failed", "invalid actor name")
					}
				case "kind":
					if e := json.Unmarshal(v, &c.Actor.Kind); e != nil || string(v) == "null" {
						return protocol.E(422, "validation_failed", "invalid actor kind")
					}
				default:
					return protocol.E(400, "unknown_property", "unknown actor field")
				}
			}
		case "runtime":
			if string(b) == "null" {
				c.Runtime = protocol.Runtime{}
				break
			}
			var fields map[string]*string
			if e := protocol.Decode(b, &fields); e != nil {
				return protocol.E(422, "validation_failed", "invalid runtime")
			}
			for key, v := range fields {
				x := ""
				if v != nil {
					x = *v
				}
				switch key {
				case "vendor":
					c.Runtime.Vendor = x
				case "session_id":
					c.Runtime.SessionID = x
				default:
					return protocol.E(400, "unknown_property", "unknown runtime field")
				}
			}
		case "output_format":
			if e := json.Unmarshal(b, &c.OutputFormat); e != nil {
				return protocol.E(422, "validation_failed", "invalid output format")
			}
		case "project_id":
			if e := json.Unmarshal(b, &c.ProjectID); e != nil {
				return protocol.E(422, "validation_failed", "invalid project selector")
			}

		default:
			return protocol.E(400, "unknown_property", "unknown client property: "+k)
		}
	}
	if e := validateClient(*c); e != nil {
		return protocol.E(422, "validation_failed", e.Error())
	}
	return nil
}
func (s *Server) connect(w http.ResponseWriter, r *http.Request) error {
	var p map[string]json.RawMessage
	if e := s.Body(r, &p); e != nil {
		return e
	}
	var c protocol.Client
	status := 201
	changed := true
	if b, ok := p["client_id"]; ok {
		var id string
		if e := json.Unmarshal(b, &id); e != nil {
			return protocol.E(422, "validation_failed", "invalid client_id")
		}
		delete(p, "client_id")
		var e error
		c, e = s.client(id)
		if e != nil {
			return e
		}
		if len(p) > 0 {
			if e = precondition(r, c); e != nil {
				return e
			}
		}
		before, _ := json.Marshal(c)
		if e = s.patchClient(&c, p); e != nil {
			return e
		}
		c.Status = "connected"
		after, _ := json.Marshal(c)
		changed = string(before) != string(after)
		if changed {
			c.StateRevision++
		}
		status = 200
	} else {
		c = protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected"}
		if e := s.patchClient(&c, p); e != nil {
			return e
		}

	}
	var cs changes
	if changed {
		cs.client = &c
	}
	return s.commitResponse(w, status, map[string]any{"client": c, "meta": s.Meta()}, cs)
}

func (s *Server) patchClient(c *protocol.Client, p map[string]json.RawMessage) error {
	if b, ok := p["project_id"]; ok && string(b) != "null" {
		var selector string
		if json.Unmarshal(b, &selector) != nil {
			return protocol.E(422, "validation_failed", "invalid project selector")
		}
		project, e := s.resolveProject(selector)
		if e != nil {
			return e
		}
		p["project_id"], _ = json.Marshal(project.ID)
	}
	return patch(c, p)
}
