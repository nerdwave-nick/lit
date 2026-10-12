package service

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// durableBody is a durable request's body as executeProjects checks it:
// decoded, then its intent canonicalized, hashed and decoded again. It
// depends on the body alone, so a write computes it before it waits for the
// write lock (predecode), and the handler reports its errors at the same
// point and in the same order as if it decoded the body itself.
type durableBody struct {
	req       protocol.DurableRequest
	bodyErr   error // the body's decoding error, as Body reports it
	canonErr  error // the intent's canonicalization error
	canonical []byte
	hash      string
	intent    protocol.Intent
}

func decodeDurable(b []byte, limit int64) *durableBody {
	d := &durableBody{}
	if d.bodyErr = decodeBody(b, limit, &d.req); d.bodyErr != nil {
		return d
	}
	if d.canonical, d.hash, d.canonErr = protocol.Canonical(d.req.Intent); d.canonErr != nil {
		return d
	}
	_ = protocol.Decode(d.canonical, &d.intent) // canonical JSON of a decoded intent
	return d
}

// durableKey is the request context key of a predecoded durableBody.
type durableKey struct{}

// predecode attaches the decoded durable body to a write that may execute
// one, so its decoding runs outside the write lock.
func (s *Server) predecode(r *http.Request, body []byte) *http.Request {
	if r.Method != "POST" && r.Method != "PATCH" {
		return r
	}
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/"), "/")
	switch segments[0] {
	case "transactions", "projects", "issues", "comments", "milestones":
	default:
		return r
	}
	if last := segments[len(segments)-1]; last == "claim" || last == "renew" || last == "release" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), durableKey{}, decodeDurable(body, s.Config.Limits.RequestBytes)))
}

// durable returns the request's durable body: predecoded, or decoded now.
func (s *Server) durable(r *http.Request) *durableBody {
	if d, ok := r.Context().Value(durableKey{}).(*durableBody); ok {
		return d
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, s.Config.Limits.RequestBytes+1))
	if e != nil {
		return &durableBody{bodyErr: protocol.E(400, "invalid_json", "cannot read JSON body")}
	}
	return decodeDurable(b, s.Config.Limits.RequestBytes)
}
