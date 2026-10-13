package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Claims have acquisition time rather than durable created/updated revisions.
// The generic created_at ordering maps to acquired_at, with issue UUID tie-break.
func (s *Server) claimPage(values url.Values) (ProjectPage, error) {
	out := ProjectPage{Items: []any{}}
	owner, id, direction, cursor := "", "", "asc", ""
	limit := s.Config.Limits.DefaultPage
	all := false
	for k, vs := range values {
		if len(vs) != 1 {
			return out, invalid("duplicate claim query parameter")
		}
		v := vs[0]
		switch k {
		case "owner_client_id":
			owner = v
			if v != "" && !protocol.ValidUUID(v) {
				return out, invalid("invalid owner_client_id")
			}
		case "issue_id":
			id = v
			if v != "" && !protocol.ValidUUID(v) {
				return out, invalid("invalid issue_id")
			}
		case "sort":
			if strings.ReplaceAll(v, "-", "_") != "created_at" {
				return out, protocol.E(400, "unsupported_filter", "claims sort supports created_at (acquisition time)")
			}
		case "direction":
			if v != "asc" && v != "desc" {
				return out, invalid("invalid direction")
			}
			direction = v
		case "limit":
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > s.Config.Limits.MaxPage {
				return out, invalid("invalid limit")
			}
			limit = n
		case "cursor":
			cursor = v
		case "all":
			if v != "true" && v != "false" {
				return out, invalid("invalid all")
			}
			all = v == "true"
		default:
			return out, protocol.E(400, "unsupported_filter", "unknown claim filter")
		}
	}
	if all && (values.Has("limit") || values.Has("cursor")) {
		return out, invalid("all conflicts with pagination")
	}
	claims, e := s.liveClaims(owner)
	if e != nil {
		return out, e
	}
	key := func(c protocol.Claim) string {
		stamp, _ := time.Parse(time.RFC3339Nano, c.AcquiredAt)
		return stamp.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	sort.Slice(claims, func(i, j int) bool {
		a, b := key(claims[i])+claims[i].IssueID, key(claims[j])+claims[j].IssueID
		if direction == "desc" {
			return a > b
		}
		return a < b
	})
	hashBytes := sha256.Sum256(mustJSON(struct {
		Owner, Issue, Direction string
		Limit                   int
		All                     bool
	}{owner, id, direction, limit, all}))
	hash := hex.EncodeToString(hashBytes[:])
	var position struct {
		Hash string `json:"hash"`
		Key  string `json:"key"`
		ID   string `json:"id"`
	}
	if cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || protocol.Decode(raw, &position) != nil || position.Hash != hash || !protocol.ValidUUID(position.ID) {
			return out, protocol.E(400, "invalid_cursor", "cursor does not match query")
		}
		if _, e = time.Parse("2006-01-02T15:04:05.000000000Z", position.Key); e != nil {
			return out, protocol.E(400, "invalid_cursor", "invalid claim cursor key")
		}
	}
	for _, claim := range claims {
		if id != "" && id != claim.IssueID {
			continue
		}
		k := key(claim)
		if position.ID != "" && (direction == "asc" && (k < position.Key || k == position.Key && claim.IssueID <= position.ID) || direction == "desc" && (k > position.Key || k == position.Key && claim.IssueID >= position.ID)) {
			continue
		}
		if !all && len(out.Items) == limit {
			last := out.Items[len(out.Items)-1].(protocol.Claim)
			position.Hash = hash
			position.Key = key(last)
			position.ID = last.IssueID
			v := base64.RawURLEncoding.EncodeToString(mustJSON(position))
			out.NextCursor = &v
			break
		}
		out.Items = append(out.Items, claim)
		if len(out.Items) > s.Config.Limits.ExpandedRecords {
			return out, protocol.E(413, "limit_exceeded", "claim snapshot too large")
		}
	}
	return out, nil
}
