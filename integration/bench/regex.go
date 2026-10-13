package bench

import "github.com/nerdwave-nick/lit/internal/protocol"

// RegexQueries are the regex searches (lit grep -E) measured, all over all
// projects and case-insensitive. The first four require a literal of three
// or more characters, which prefilters candidates through the trigram index:
// the user story's alternation (rare), an identifier with a class (common), an
// alternation of identifiers, and no match. The last two require none and
// read every record: headings (common, so the scan stops early) and a
// pattern that matches nothing (a full scan, the slowest case).
var RegexQueries = []string{
	`econn(refused|reset)`,
	`func \(s \*Server\) [A-Z]\w*`,
	`storage_busy|search_timeout`,
	`no-such-(needle|pin)-anywhere`,
	`^#+ \w+`,
	`\d{5}-\d{5}-\d{5}`,
}

// Regex runs lit grep -E over all projects as operation name.
func (c *Client) Regex(name, pattern string) (int, error) {
	env, err := c.single(name, "POST", "/v1/search", protocol.SearchRequest{Scope: protocol.SearchScope{AllProjects: true}, Query: pattern, Regex: true})
	return len(env.Items), err
}

// measureRegex runs perQuery searches of each of RegexQueries, recorded as
// "regex: PATTERN" and together as the "regex" operation. tolerate decides
// whether a failure ends the measurement.
func measureRegex(c *Client, rec *Recorder, perQuery int, tolerate func(error) error) error {
	for _, q := range RegexQueries {
		for range perQuery {
			_, failed := c.Regex("regex: "+q, q)
			if err := tolerate(failed); err != nil {
				return err
			}
		}
		for _, d := range rec.Samples("regex: " + q) {
			rec.Add("regex", d)
		}
	}
	return nil
}
