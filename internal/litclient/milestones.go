package litclient

import (
	"context"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// MilestoneProgress is the service's read-only projection of a milestone's
// members, computed at read time: it is not part of the durable record. An
// empty milestone has Total 0; it is an empty scope, not a completed one.
type MilestoneProgress struct {
	Total       int `json:"total"`
	Open        int `json:"open"`
	Closed      int `json:"closed"`
	BlockedOpen int `json:"blocked_open"`
	ClaimedOpen int `json:"claimed_open"`
	// ExternalBlockers are open blockers of open members that are not
	// members themselves.
	ExternalBlockers []string `json:"external_blockers"`
}

// MilestoneView is a milestone as a snapshot read returns it: the durable
// record with its progress.
type MilestoneView struct {
	protocol.Milestone
	Progress MilestoneProgress `json:"progress"`
}

// MilestonePage is one page of milestones with their progress.
type MilestonePage struct {
	Items      []MilestoneView `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

// Milestones reads one page of milestones matching q (a milestone query, as
// for List) with their progress. It is a read and journals nothing.
func (s *Session) Milestones(ctx context.Context, q Query) (MilestonePage, error) {
	defer s.bind(ctx)()
	typed := struct {
		Type string `json:"type"`
		Query
	}{"milestones", q}
	var page MilestonePage
	if e := s.Call("POST", "/v1/snapshots", map[string]any{"schema_version": 1, "query": typed}, nil, &page, false); e != nil {
		return MilestonePage{}, e
	}
	if page.Items == nil {
		return MilestonePage{}, protocol.E(500, "invalid_response", "snapshot omitted items")
	}
	for _, m := range page.Items {
		if !validRecord(m.Milestone) {
			return MilestonePage{}, protocol.E(500, "invalid_response", "snapshot returned an invalid milestones record")
		}
	}
	return page, nil
}
