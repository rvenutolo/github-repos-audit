package github

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// discoverPageSize is GitHub's maximum, so a typical personal account's
// repositories arrive in one request.
const discoverPageSize = 100

// maxDiscoverPages stops a paging bug from looping forever. At the page size
// above it is ten thousand repositories, which no personal account reaches.
const maxDiscoverPages = 100

// discovered is the slice of the /user/repos payload this tool reads. The
// response carries a hundred other fields; naming only these three is
// deliberate, because security_and_analysis is absent from the list payload
// even for a public repository and folding any setting into discovery would
// therefore read a value that is not there.
type discovered struct {
	Name     string `json:"name"`
	Fork     bool   `json:"fork"`
	Archived bool   `json:"archived"`
}

// Discover lists every owned repository that is neither a fork nor archived,
// sorted case-insensitively. Forks and archived repositories are out of scope:
// an archived repository cannot be changed without unarchiving it, so its gaps
// would dominate the report without ever being actionable.
func (c *Client) Discover(ctx context.Context) ([]string, error) {
	names := make([]string, 0, discoverPageSize)
	path := "/user/repos?affiliation=owner&per_page=" + strconv.Itoa(discoverPageSize)

	for page := 1; path != ""; page++ {
		if page > maxDiscoverPages {
			return nil, fmt.Errorf("discover: more than %d pages of repositories", maxDiscoverPages)
		}
		var batch []discovered
		resp, err := c.getJSON(ctx, path, &batch)
		if err != nil {
			return nil, err
		}
		for _, r := range batch {
			if r.Fork || r.Archived {
				continue
			}
			names = append(names, r.Name)
		}
		path = nextPage(resp.header.Get("Link"))
	}

	slices.SortFunc(names, compareFold)
	c.log.InfoContext(ctx, "discovered repositories", "count", len(names))
	return names, nil
}

// nextPage returns the path and query of the Link header's rel="next" entry,
// or "" when there is none. Only the path and query are kept: the header names
// api.github.com even when the client was pointed somewhere else, and
// following the host GitHub printed would take a test's requests off its own
// httptest server and onto the real API. A target that does not reduce to a
// rooted path under the base URL is answered with "" as well, which the caller
// already reads as the last page: see rootedPath.
func nextPage(link string) string {
	for part := range strings.SplitSeq(link, ",") {
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		var rel string
		for _, s := range segments[1:] {
			if v, ok := strings.CutPrefix(strings.TrimSpace(s), "rel="); ok {
				rel = strings.Trim(v, `"`)
			}
		}
		if rel != "next" {
			continue
		}
		u, err := url.Parse(strings.Trim(strings.TrimSpace(segments[0]), "<>"))
		if err != nil {
			return ""
		}
		p := u.EscapedPath()
		if !rootedPath(p) {
			return ""
		}
		if u.RawQuery == "" {
			return p
		}
		return p + "?" + u.RawQuery
	}
	return ""
}

// rootedPath reports whether p is a path the caller may append to the base
// URL. It must be rooted, because the caller concatenates rather than
// resolves and "repos" would land against "https://api.github.comrepos". It
// must not begin with a second slash, because the result is then read as an
// authority, which is how an empty one in the header turns the rest of the
// path into a host. It must hold no ".." segment, because the base URL is
// meant to be a floor and climbing above it is never a page of results.
func rootedPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}
	return !slices.Contains(strings.Split(p, "/"), "..")
}
