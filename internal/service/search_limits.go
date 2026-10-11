package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Limits shared by every search mode: literal lit grep (POST /v1/search) and
// the regex and ranked searches built on it. A search route is classified
// searchAccess in the route table, so it runs through search and its
// deadline; its handler checks each pattern or query with checkSearchText
// before searching.

// MaxSearchBytes is the longest pattern or query, in UTF-8 bytes.
const MaxSearchBytes = 1000

// errSearchDeadline is the cause of a search context that reached the
// search deadline.
var errSearchDeadline = errors.New("search deadline exceeded")

// searchTimeout is the configured search deadline; an unset one is the
// default, so no search runs without a deadline.
func (s *Server) searchTimeout() time.Duration {
	if s.Config.SearchTimeout <= 0 {
		return DefaultSearchTimeout
	}
	return time.Duration(s.Config.SearchTimeout)
}

// checkSearchText refuses a pattern or query (named by field) longer than
// MaxSearchBytes with validation_failed.
func checkSearchText(field, text string) error {
	if len(text) > MaxSearchBytes {
		return invalid(fmt.Sprintf("%s must be at most %d bytes", field, MaxSearchBytes))
	}
	return nil
}

// searchTimedOut is the search_timeout error of a search that reached its
// deadline limit. Its status is 504: the service could not finish in time,
// and the same request is likely to time out again, unlike a 503 storage_busy.
func searchTimedOut(limit time.Duration) *protocol.Error {
	return protocol.E(504, "search_timeout", fmt.Sprintf("search exceeded its %s deadline; narrow the query or its scope (one project instead of all projects)", limit))
}

// search runs f as one read whose work is bounded by the search deadline,
// counted from the moment its snapshot opens: waiting for a snapshot over the
// read cap does not count, and stays bounded by the request. A failure once
// the deadline has passed is search_timeout; a request that ends first stays
// storage_busy, and protocol errors are kept.
func (s *Server) search(ctx context.Context, f func(*Server) error) error {
	limit := s.searchTimeout()
	return s.boundedSnapshot(ctx, limit, errSearchDeadline, func(rs *Server) error {
		e := f(rs)
		var pe *protocol.Error
		if e != nil && !errors.As(e, &pe) && errors.Is(context.Cause(rs.ctx()), errSearchDeadline) {
			return searchTimedOut(limit)
		}
		return e
	})
}
