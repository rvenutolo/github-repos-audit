package github

import (
	"context"
	"fmt"
)

// viewerPath names the account the token itself belongs to.
const viewerPath = "/user"

// Viewer returns the login of the account opts.Token authenticates as.
//
// That login is the owner every other call reads. Discovery lists
// /user/repos, so the token's own account is the only one this tool can
// audit, and asking the token beats a configured name that could disagree
// with it. opts.Owner is ignored.
func Viewer(ctx context.Context, opts Options) (string, error) {
	c, err := newClient(opts)
	if err != nil {
		return "", err
	}
	var body struct {
		Login string `json:"login"`
	}
	if _, err := c.getJSON(ctx, viewerPath, &body); err != nil {
		return "", err
	}
	if body.Login == "" {
		return "", fmt.Errorf("%s: response carries no login", viewerPath)
	}
	return body.Login, nil
}
