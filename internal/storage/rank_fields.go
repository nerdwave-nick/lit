package storage

import (
	"context"
	"strings"
)

// The highlight markers that rank_index's highlight() puts around matched
// words. They are Unicode noncharacters, reserved for internal use; a field
// whose text contains one is reported without match offsets.
const (
	markOpen  = "﷐"
	markClose = "﷑"
)

// rankedFields reads the matching fields of a result: the columns of its own
// row that hold a matched word, then its best matching comments.
func (q queries) rankedFields(ctx context.Context, match string, r *RankedResult) error {
	rows := "SELECT seq FROM rank_text WHERE owner_type = ?4 AND owner_id = ?5"
	if r.Type == Issues {
		rows = "SELECT seq FROM rank_text WHERE issue_id = ?5 AND issue_id <> ''"
	}
	query := `SELECT t.owner_type, t.owner_id, highlight(rank_index, 0, ?2, ?3), highlight(rank_index, 1, ?2, ?3), highlight(rank_index, 2, ?2, ?3),
		t.title, t.body, t.comment
		FROM rank_index JOIN rank_text t ON t.seq = rank_index.rowid
		WHERE rank_index MATCH ?1 AND rank_index.rowid IN (` + rows + `)
		ORDER BY bm25(rank_index, ` + rankWeights + `), t.owner_type, t.owner_id`
	var own, comments []RankedField
	e := q.each(ctx, query, []any{match, markOpen, markClose, r.Type, r.ID}, func(s rowScanner) error {
		var typ, id string
		var marked, text [3]string
		if e := s.Scan(&typ, &id, &marked[0], &marked[1], &marked[2], &text[0], &text[1], &text[2]); e != nil {
			return e
		}
		if typ == Comments {
			r.CommentMatches++
			if len(comments) < MaxRankedComments {
				comments = append(comments, rankedField("comment", id, marked[2], text[2]))
			}
			return nil
		}
		for i, name := range []string{"title", bodyField(typ)} {
			if text[i] != "" && (strings.Contains(marked[i], markOpen) || markerIn(text)) {
				own = append(own, rankedField(name, "", marked[i], text[i]))
			}
		}
		return nil
	})
	r.Fields = append(own, comments...)
	return e
}

// markerIn reports whether one of the texts contains a highlight marker.
func markerIn(texts [3]string) bool {
	for _, t := range texts {
		if strings.Contains(t, markOpen) || strings.Contains(t, markClose) {
			return true
		}
	}
	return false
}

// rankedField is the field name of text, with the byte ranges of the words
// that highlight() marked in marked. When text holds a marker itself, the
// markers cannot be told apart, so the field has no ranges.
func rankedField(name, commentID, marked, text string) RankedField {
	f := RankedField{Name: name, CommentID: commentID, Text: text, Matches: [][2]int{}}
	if strings.Contains(text, markOpen) || strings.Contains(text, markClose) {
		return f
	}
	var plain strings.Builder
	start := -1
	for i := 0; i < len(marked); {
		switch {
		case strings.HasPrefix(marked[i:], markOpen):
			start = plain.Len()
			i += len(markOpen)
		case strings.HasPrefix(marked[i:], markClose):
			if start >= 0 {
				f.Matches = append(f.Matches, [2]int{start, plain.Len()})
			}
			start = -1
			i += len(markClose)
		default:
			plain.WriteByte(marked[i])
			i++
		}
	}
	if plain.String() != text {
		f.Matches = [][2]int{}
	}
	return f
}
