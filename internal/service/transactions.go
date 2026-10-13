package service

import (
	"net/http"
	"sort"
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func invalid(msg string) error { return protocol.E(422, "validation_failed", msg) }
func (s *Server) prepareProjects(req protocol.PrepareRequest, c protocol.Client) (protocol.Prepared, error) {
	var out protocol.Prepared
	if req.SchemaVersion != 0 && req.SchemaVersion != 1 {
		return out, invalid("unsupported schema_version")
	}
	kind := strings.TrimPrefix(req.Operation, "project.")
	if kind != "create" && kind != "update" {
		return out, invalid("unsupported operation")
	}
	if len(req.Items) == 0 {
		return out, invalid("empty mutation batch")
	}
	if len(req.Items) > s.Config.Limits.ExplicitItems {
		return out, protocol.E(413, "limit_exceeded", "too many explicit items")
	}
	intent := protocol.Intent{Force: req.Force, SchemaVersion: 1, Operation: "transaction", ServiceID: s.DB.ServiceID(), Actor: protocol.DurableActor{ClientID: c.ClientID, Name: c.Actor.Name, Kind: c.Actor.Kind}}
	ops := map[string]protocol.Operation{}
	refs := map[string]protocol.ObjectRef{}
	for _, item := range req.Items {
		if e := item.ValidateShape(kind); e != nil {
			return out, invalid(e.Error())
		}
		id := item.ID
		var rev *int64
		set := item.Set
		if kind == "create" {
			if !protocol.ValidUUID(id) || item.Target != "" || item.ExpectedRevision != nil || item.Title == nil || set.Title != nil || set.Description != nil || set.RepositoryRefs != nil || len(item.Clear) > 0 || len(item.Add.RepositoryRefs) > 0 || len(item.Remove.RepositoryRefs) > 0 {
				return out, invalid("invalid project creation")
			}
			set.Title = item.Title
			set.Description = item.Content
			members := append([]string{}, item.RepositoryRefs...)
			set.RepositoryRefs = &members
		} else {
			if id != "" || item.Title != nil || item.Content != nil || item.RepositoryRefs != nil {
				return out, invalid("invalid project update fields")
			}
			p, e := s.resolveProject(item.Target)
			if e != nil {
				return out, e
			}
			id = p.ID
			r := p.Revision
			rev = &r
			if item.ExpectedRevision != nil {
				rev = item.ExpectedRevision
			}
			for _, clear := range item.Clear {
				switch clear {
				case "content", "description":
					empty := ""
					if set.Description != nil && *set.Description != "" {
						return out, invalid("clear conflicts with description")
					}
					set.Description = &empty
				case "repositories", "repository_refs":
					empty := []string{}
					if set.RepositoryRefs != nil && len(*set.RepositoryRefs) > 0 {
						return out, invalid("clear conflicts with repositories")
					}
					set.RepositoryRefs = &empty
				default:
					return out, invalid("unknown clear field")
				}
			}
		}
		op := protocol.Operation{Type: "projects", ID: id, Kind: kind, Set: set, Add: item.Add, Remove: item.Remove}
		ref := protocol.ObjectRef{Type: "projects", ID: id, ExpectedRevision: rev}
		check := intent
		check.Operations = []protocol.Operation{op}
		check.Targets = []protocol.ObjectRef{ref}
		canonical, _, e := protocol.Canonical(check)
		if e != nil {
			return out, invalid(e.Error())
		}
		var normalized protocol.Intent
		protocol.Decode(canonical, &normalized)
		op = normalized.Operations[0]
		if old, ok := ops[id]; ok {
			if !same(ref, refs[id]) {
				return out, invalid("incompatible expected revisions")
			}
			merged, e := mergeProjectOperation(old, op)
			if e != nil {
				return out, e
			}
			op = merged
		}
		ops[id] = op
		refs[id] = ref
	}
	for id, op := range ops {
		intent.Operations = append(intent.Operations, op)
		intent.Targets = append(intent.Targets, refs[id])
	}
	b, h, e := protocol.Canonical(intent)
	if e != nil {
		return out, invalid(e.Error())
	}
	if e = protocol.Decode(b, &intent); e != nil {
		return out, e
	}
	if _, _, e = s.recordTransaction(intent, h, c); e != nil {
		return out, e
	}
	return protocol.Prepared{SchemaVersion: 1, ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Intent: intent, CanonicalJSON: string(b), RequestHash: h, RequiredClaims: []protocol.RequiredClaim{}}, nil
}
func mergeProjectOperation(a, b protocol.Operation) (protocol.Operation, error) {
	if a.Kind != b.Kind {
		return a, invalid("incompatible operations")
	}
	for _, v := range []struct {
		dst **string
		src *string
	}{{&a.Set.Title, b.Set.Title}, {&a.Set.Description, b.Set.Description}} {
		if v.src != nil {
			if *v.dst != nil && **v.dst != *v.src {
				return a, invalid("contradictory scalar assignments")
			}
			*v.dst = v.src
		}
	}
	if b.Set.RepositoryRefs != nil {
		if a.Set.RepositoryRefs != nil {
			aa, bb := append([]string{}, (*a.Set.RepositoryRefs)...), append([]string{}, (*b.Set.RepositoryRefs)...)
			sort.Strings(aa)
			sort.Strings(bb)
			if !same(aa, bb) {
				return a, invalid("contradictory repository replacements")
			}
		}
		a.Set.RepositoryRefs = b.Set.RepositoryRefs
	}
	a.Add.RepositoryRefs = union(a.Add.RepositoryRefs, b.Add.RepositoryRefs)
	a.Remove.RepositoryRefs = union(a.Remove.RepositoryRefs, b.Remove.RepositoryRefs)
	return a, nil
}
func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, v := range b {
		found := false
		for _, x := range out {
			found = found || v == x
		}
		if !found {
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) executeProjects(w http.ResponseWriter, r *http.Request, c protocol.Client, routeID, kind string) error {
	d := s.durable(r)
	if d.bodyErr != nil {
		return d.bodyErr
	}
	req := d.req
	if req.SchemaVersion != 0 && req.SchemaVersion != 1 {
		return invalid("unsupported schema_version")
	}
	if d.canonErr != nil {
		return invalid(d.canonErr.Error())
	}
	hash, i := d.hash, d.intent
	if hash != req.RequestHash {
		return protocol.E(422, "request_hash_mismatch", "canonical intent hash differs")
	}
	if kind == "relationships" {
		if e := validateRelationshipRoute(i.Operations, routeID); e != nil {
			return e
		}
	} else if kind != "" {
		expectedType := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/"), "/")[0]
		if len(i.Operations) != 1 || i.Operations[0].Type != expectedType {
			return invalid("route resource does not match intent")
		}
		if len(i.Operations) != 1 || i.Operations[0].Kind != kind || (routeID != "" && i.Operations[0].ID != routeID) {
			return invalid("route does not match intent")
		}
	}
	cs, result, e := s.recordTransaction(i, hash, c, req.Claims...)
	if e != nil {
		return e
	}
	status := 200
	if kind == "create" {
		status = 201
	}
	return s.commitResponse(w, status, result, cs)
}
