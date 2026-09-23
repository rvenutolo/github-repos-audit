package github

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// fetchHistoryPage is how walkHistory asks for the page after cursor. It is a
// parameter so the walk's guards can be tested without a server.
type fetchHistoryPage func(ctx context.Context, cursor string) (*history, error)

// walkHistory follows first's pages to the end and returns every distinct
// identity, exactly as spelled, sorted by name then email in byte order. A
// null actor is skipped: there is nobody to record. A null name or email is
// recorded as "", because the other half is still a fact.
//
// Nothing here folds case, trims or matches a pattern. Two spellings of one
// address are two identities, because whether that difference matters is a
// judgement, and judgements are internal/rules' alone.
func walkHistory(ctx context.Context, first *history, next fetchHistoryPage) ([]audit.Identity, error) {
	seen := map[audit.Identity]struct{}{}
	for page := first; page != nil; {
		for _, n := range page.Nodes {
			for _, a := range []*gitActor{n.Author, n.Committer} {
				if a != nil {
					seen[audit.Identity{Name: deref(a.Name), Email: deref(a.Email)}] = struct{}{}
				}
			}
		}
		if !page.PageInfo.HasNextPage {
			break
		}
		cursor := deref(page.PageInfo.EndCursor)
		if cursor == "" {
			// Asking again without a cursor would return page one forever.
			return nil, fmt.Errorf("history: %w: next page promised but no end cursor", errGraphQL)
		}
		var err error
		if page, err = next(ctx, cursor); err != nil {
			return nil, err
		}
	}
	out := slices.Collect(maps.Keys(seen))
	slices.SortFunc(out, func(a, b audit.Identity) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Email, b.Email)
	})
	return out, nil
}

// deref reads an optional GraphQL string, treating null as empty.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// identities walks the default branch's history. An empty repository has no
// history and so no identities; that is an answer, not an error. A live
// answer for a non-empty repository always carries history, because the query
// asks for it; its absence is tolerated only so hand-built fixtures without it
// still decode.
//
// Every later page is asked of the head commit's oid rather than the branch:
// the first page already describes that commit, and a push landing mid-walk
// would otherwise splice a second history onto the first.
func (c *Client) identities(ctx context.Context, name string, r *repository) ([]audit.Identity, error) {
	if r.DefaultBranchRef == nil || r.DefaultBranchRef.Target == nil || r.DefaultBranchRef.Target.History == nil {
		return nil, nil
	}
	oid := r.DefaultBranchRef.Target.OID
	return walkHistory(ctx, r.DefaultBranchRef.Target.History, func(ctx context.Context, cursor string) (*history, error) {
		var data struct {
			Repository *struct {
				Object *struct {
					History *history `json:"history"`
				} `json:"object"`
			} `json:"repository"`
		}
		vars := map[string]string{"owner": c.owner, "name": name, "oid": oid, "cursor": cursor}
		if err := c.postGraphQL(ctx, historyQuery, vars, &data); err != nil {
			return nil, err
		}
		// The oid came from this run's own answer, so a null here means the
		// commit vanished between two requests — a force-push that garbage
		// collected it, or a deleted repository. Recording what was walked so
		// far would present half a history as the whole.
		if data.Repository == nil || data.Repository.Object == nil || data.Repository.Object.History == nil {
			return nil, fmt.Errorf("history: %w: commit not found", errGraphQL)
		}
		return data.Repository.Object.History, nil
	})
}
