package litclient

import (
	"context"
	"errors"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Code returns the protocol error code carried by err, such as
// "revision_conflict", "claim_conflict", "claim_expired" or
// "outcome_uncertain". It is "" for nil and for errors that are not protocol
// errors. Callers branch on codes, never on HTTP status; errors.As with a
// *protocol.Error also exposes the message and details (an uncertain mutation
// carries its retained "request_id").
func Code(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// bind gives one typed operation its own context and restores the session's
// previous context afterwards. A nil ctx keeps the session's context.
func (s *Session) bind(ctx context.Context) (restore func()) {
	old := s.Context
	switch {
	case ctx != nil:
		s.Context = ctx
	case s.Context == nil:
		s.Context = context.Background()
	}
	return func() { s.Context = old }
}
