package cli

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func (a *App) transactionStatus() int {
	files := []string{}
	explicit := a.Args.Has("file")
	if explicit {
		files = append(files, a.Args.One("file"))
	} else {
		entries, e := os.ReadDir(filepath.Join(a.StateDir, "pending"))
		if e != nil && !os.IsNotExist(e) {
			return a.Error(protocol.E(500, "invalid_local_state", e.Error()))
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
				return a.Error(protocol.E(500, "invalid_local_state", "unrecognized pending evidence: "+entry.Name()))
			}
			files = append(files, filepath.Join(a.StateDir, "pending", entry.Name()))
		}
	}

	// Validate every envelope before network calls or cleanup. A corrupt record is
	// evidence; never silently skip it or delete earlier items before reporting it.
	records := make([]Pending, len(files))
	for n, path := range files {
		p, e := litclient.ReadPending(path)
		if e != nil {
			return a.Error(protocol.E(500, "invalid_local_state", e.Error()))
		}
		if !explicit && filepath.Base(path) != p.RequestID+".json" {
			return a.Error(protocol.E(500, "invalid_local_state", "pending filename differs from request identity"))
		}
		records[n] = p
	}
	var scope *Mapping
	if a.Args.Has("session") {
		m, e := litclient.ReadMapping(litclient.MappingPath(a.StateDir, a.Args.One("session")))
		if e != nil {
			return a.Error(protocol.E(500, "invalid_local_state", e.Error()))
		}
		scope = &m
	}
	result := Result{Items: []any{}}
	uncertainOutcome := false
	for n, p := range records {
		if scope != nil && (p.ServiceID != scope.ServiceID || p.ClientID != scope.ClientID) {
			if explicit {
				return a.Error(protocol.E(409, "wrong_client", "pending request differs from selected session"))
			}
			continue
		}
		timeout := 30 * time.Second
		if d, e := time.ParseDuration(a.Args.One("timeout")); e == nil {
			timeout = d
		}
		deadline := time.Now().Add(timeout)
		if d, ok := a.Context.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		unlock, e := litclient.AcquireLock(filepath.Join(a.StateDir, "locks", "pending", p.RequestID+".lock"), deadline)
		if e != nil {
			result.Items = append(result.Items, protocol.TransactionStatus{RequestID: p.RequestID, Status: "uncertain", Proof: "request_still_active"})
			uncertainOutcome = true
			continue
		}
		// Re-read after locking: an active original CLI may have resolved and removed it.
		p, e = litclient.ReadPending(files[n])
		if os.IsNotExist(e) && !explicit {
			unlock()
			continue
		}
		if e != nil {
			unlock()
			return a.Error(protocol.E(500, "invalid_local_state", e.Error()))
		}
		remote := *a
		remote.Mapping = Mapping{SchemaVersion: 1, ClientID: p.ClientID, ServiceID: p.ServiceID, Endpoint: p.Endpoint}
		remote.Endpoint = strings.TrimRight(p.Endpoint, "/")
		override := a.Args.One("endpoint")
		if override == "" {
			override = os.Getenv("LIT_ENDPOINT")
		}
		if override != "" {
			q := p
			q.Endpoint = override
			if e = litclient.ValidatePending(q); e != nil {
				unlock()
				return a.Error(protocol.E(400, "invalid_endpoint", e.Error()))
			}
			remote.Endpoint = strings.TrimRight(override, "/")
		}
		var meta protocol.Meta
		e = remote.Call("GET", "/v1/meta", nil, nil, &meta, false)
		if e == nil && (meta.ServiceID != p.ServiceID || meta.APIMajor != 1) {
			unlock()
			return a.Error(protocol.E(409, "wrong_service", "endpoint is bound to a different service"))
		}
		v := protocol.TransactionStatus{RequestID: p.RequestID, Status: "uncertain", Proof: "service_unavailable"}
		if e == nil {
			remote.Meta = meta
			v, e = remote.Reconcile(p)
		}
		if e != nil {
			v = protocol.TransactionStatus{RequestID: p.RequestID, Status: "uncertain", Proof: "reconciliation_unavailable"}
			if pe, ok := e.(*protocol.Error); ok {
				if pe.Status == 400 || pe.Status == 422 || pe.Code == "wrong_client" || pe.Code == "wrong_service" {
					unlock()
					return a.Error(e)
				}
				v.Proof = pe.Code
			}
		}
		// The status surface reports evidence, not synthesized HTTP bodies.
		v.Response = nil
		result.Items = append(result.Items, v)
		a.ServerTime = remote.ServerTime
		if v.Status == "uncertain" {
			uncertainOutcome = true
		} else if !explicit {
			if e = os.Remove(files[n]); e != nil {
				unlock()
				return a.Error(protocol.E(500, "local_state_error", e.Error()))
			}
		}
		unlock()
	}
	code := a.Print(result)
	if code == 0 && uncertainOutcome {
		return 4
	}
	return code
}
