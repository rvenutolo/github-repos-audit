package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// contentsRequest names one file to read with the contents API. Renovate
// presets, a marketplace's catalogue and a plugin's manifest at a pinned sha
// are all read this way, so the request belongs to none of them.
type contentsRequest struct {
	Repo string // repository name within the audited account
	Path string // file path within it
	Ref  string // git ref; empty means the default branch
}

// contentsAnswer is one read's outcome: the file's text, or why there is
// none. notFound is a 404 (repository, path or ref missing), kept apart from
// problem because each caller says something of its own about an absent
// file, while every other problem is shown as written.
type contentsAnswer struct {
	text     string
	notFound bool
	problem  string
}

// problemInvalidPath is the problem fetchContents reports for a repository
// or path it will not request.
const problemInvalidPath = "invalid path"

// contentsFile is the slice of the contents API's answer this tool reads.
type contentsFile struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

// fetchContents reads one file with GET /repos/{owner}/{repo}/contents. A
// 404 is an ordinary answer. The default Accept header returns the JSON
// envelope with base64 content; a directory answers with an array, and a
// file over 1 MB with encoding "none", neither of which is a file this tool
// can read.
//
// The repository, path and ref can come from a repository's own file, so
// none is concatenated as written. The path goes through escapeSegments, its
// slashes kept as separators; a repository or path with a ".", ".." or empty
// segment would name another endpoint once the URL is normalised, so it is
// never requested and answers problemInvalidPath, which the caller records
// rather than aborting the run. The ref is query-escaped, so it cannot add a
// parameter.
func (c *Client) fetchContents(ctx context.Context, req contentsRequest) (contentsAnswer, error) {
	_, repoOK := escapeSegments(req.Repo)
	escaped, pathOK := escapeSegments(req.Path)
	if !repoOK || !pathOK {
		return contentsAnswer{problem: problemInvalidPath}, nil
	}
	path := c.repoPath(req.Repo) + "/contents/" + escaped
	if req.Ref != "" {
		path += "?ref=" + url.QueryEscape(req.Ref)
	}

	var raw json.RawMessage
	resp, err := c.getJSON(ctx, path, &raw, http.StatusNotFound)
	if err != nil {
		return contentsAnswer{}, err
	}
	if resp.status == http.StatusNotFound {
		return contentsAnswer{notFound: true}, nil
	}
	return decodeContents(raw), nil
}

// decodeContents reads a 200 answer from the contents API. Every way it can
// fail to be a file's text is a problem to record, not an error: GitHub gave
// a definite answer, it just is not a file this tool can read.
func decodeContents(raw json.RawMessage) contentsAnswer {
	var f contentsFile
	// A directory answers with an array, which does not decode into an object;
	// a symlink or a submodule answers with its own type.
	if json.Unmarshal(raw, &f) != nil || f.Type != "file" {
		return contentsAnswer{problem: "not a file"}
	}
	if f.Encoding != "base64" {
		return contentsAnswer{problem: "too large to read"}
	}
	// GitHub wraps the base64 at 60 characters with newlines.
	text, decodeErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if decodeErr != nil {
		return contentsAnswer{problem: "content is not valid base64"}
	}
	return contentsAnswer{text: string(text)}
}
