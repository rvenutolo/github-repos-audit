package github_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fixtureRoot holds the recorded API responses: what each endpoint actually
// answered when this package was written, which is half the point of keeping
// them. Three departures from a verbatim capture, all deliberate:
//
//   - The discovery pages and the repository objects carry only the fields this
//     tool reads. The full repository object also carries temp_clone_token,
//     which is a real credential and must not be committed; trimming to the
//     read fields removes it rather than redacting it in place.
//   - The blank-repo set is hand-built from the shapes above, because every
//     live repository has commits now. The spec documents what an empty one
//     answers, and one of the account's repositories was empty when this was
//     written.
//   - Bodies are re-indented for review. Nothing here is compared byte for
//     byte, so the whitespace carries no meaning.
const fixtureRoot = "testdata/api"

// testOwner is the account every fixture was captured from.
const testOwner = "gh-owner"

// call is one request the fake API saw. The read-only test reads these.
// escapedPath and rawQuery are the request as sent on the wire, which is what
// a test of the client's URL escaping has to look at: path is decoded.
type call struct {
	method      string
	path        string
	escapedPath string
	rawQuery    string
}

// scriptedFailure is one answer the fake gives instead of a fixture: a
// transient failure such as a secondary rate limit or a bad gateway, or a
// GraphQL error page, put in front of an endpoint that otherwise answers
// normally.
type scriptedFailure struct {
	status int
	header map[string]string
	body   string
}

// fakeAPI serves the recorded responses over httptest and records every request
// it is handed, so the same server can drive the collector offline and prove
// afterwards that nothing it did could have written to GitHub.
type fakeAPI struct {
	t         *testing.T
	server    *httptest.Server
	closeOnce sync.Once

	// mu guards the fields below, which the server's handler goroutines write.
	mu        sync.Mutex
	calls     []call
	documents []string
	variables []map[string]string
	failures  map[string][]scriptedFailure
	// claudeQueries counts each repository's Claude file queries, which is
	// what numbers the fixture that answers the next one.
	claudeQueries map[string]int
	// claudeFailure, when set, answers every Claude file query instead of
	// its fixture.
	claudeFailure *scriptedFailure
}

// newFakeAPI starts a server backed by testdata/api and stops it with the test.
func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()

	f := &fakeAPI{t: t, failures: map[string][]scriptedFailure{}, claudeQueries: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /graphql", f.handleGraphQL)
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, _ *http.Request) {
		f.serve(w, fixtureRoot, "user")
	})
	mux.HandleFunc("GET /user/repos", f.handleDiscover)
	mux.HandleFunc("GET /repos/{owner}/{repo}", f.fixtureHandler("repo"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/rules/branches/{branch}", f.fixtureHandler("rules-branches"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/private-vulnerability-reporting",
		f.fixtureHandler("private-vulnerability-reporting"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/automated-security-fixes",
		f.fixtureHandler("automated-security-fixes"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/permissions", f.fixtureHandler("actions-permissions"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/permissions/selected-actions",
		f.fixtureHandler("selected-actions"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/permissions/workflow", f.fixtureHandler("actions-workflow"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/permissions/access", f.fixtureHandler("actions-access"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/runs", f.handleRuns)
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/runs/{run_id}/jobs", f.handleJobs)
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/caches", f.storageListHandler("actions-caches"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/cache/usage", f.fixtureHandler("actions-cache-usage"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/cache/storage-limit",
		f.fixtureHandler("actions-cache-storage-limit"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/artifacts", f.storageListHandler("actions-artifacts"))
	mux.HandleFunc("GET /repos/{owner}/{repo}/contents/{path...}", f.handleContents)
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/trees/{sha}", f.handleTree)

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if f.serveFailure(w, r.URL.Path) {
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.close)
	return f
}

// url is where the client should be pointed.
func (f *fakeAPI) url() string { return f.server.URL }

// close shuts the server down. It is idempotent so a test that has to release
// the server before an assertion — the goroutine-leak check does — can call it
// early without racing the cleanup.
func (f *fakeAPI) close() { f.closeOnce.Do(f.server.Close) }

func (f *fakeAPI) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{
		method:      r.Method,
		path:        r.URL.Path,
		escapedPath: r.URL.EscapedPath(),
		rawQuery:    r.URL.RawQuery,
	})
}

// failNext queues answers for the next calls to path, in the order given,
// after which that path serves its fixture again.
func (f *fakeAPI) failNext(path string, answers ...scriptedFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[path] = append(f.failures[path], answers...)
}

// failClaude makes every Claude file query answer answer, which no path-keyed
// failNext can do: the query shares /graphql with the ones before it.
func (f *fakeAPI) failClaude(answer scriptedFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claudeFailure = &answer
}

// serveFailure answers with the next scripted failure for path, if there is
// one, and reports whether it did.
func (f *fakeAPI) serveFailure(w http.ResponseWriter, path string) bool {
	f.mu.Lock()
	queued := f.failures[path]
	if len(queued) == 0 {
		f.mu.Unlock()
		return false
	}
	next := queued[0]
	f.failures[path] = queued[1:]
	f.mu.Unlock()

	for k, v := range next.header {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(next.status)
	if _, err := w.Write([]byte(next.body)); err != nil {
		f.t.Errorf("write scripted failure for %s: %v", path, err)
	}
	return true
}

// callsTo counts the requests the server was handed for path.
func (f *fakeAPI) callsTo(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.path == path {
			n++
		}
	}
	return n
}

// runsCalls counts repo's runs-list requests in two: the timing reads,
// unfiltered, and the default-branch recency reads, filtered by branch. Both
// share one path, so callsTo alone cannot tell them apart.
func (f *fakeAPI) runsCalls(repo string) (timing, recency int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := "/repos/" + testOwner + "/" + repo + "/actions/runs"
	for _, c := range f.calls {
		if c.path != path {
			continue
		}
		if q, err := url.ParseQuery(c.rawQuery); err == nil && q.Has("branch") {
			recency++
		} else {
			timing++
		}
	}
	return timing, recency
}

// seen returns every request the server was handed.
func (f *fakeAPI) seen() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

// graphQLDocuments returns every document posted to /graphql.
func (f *fakeAPI) graphQLDocuments() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.documents...)
}

// graphQLVariables returns the variables of every document posted to
// /graphql, in order, so a test can see which cursor and oid a page asked for.
func (f *fakeAPI) graphQLVariables() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.variables...)
}

// handleGraphQL answers with the recorded response for the repository named in
// the request's variables, which is how one endpoint serves every repository.
func (f *fakeAPI) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	var body struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}
	// Read whole before it is parsed, so the two ways a body can be bad stay
	// apart. A read that fails is a client that went away mid-request, which
	// Collect does to every request in flight once one repository has failed:
	// there is nobody to answer and nothing wrong with the test. A body that
	// arrived whole and does not parse is a request the client built wrong.
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		f.t.Errorf("decode graphql request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.documents = append(f.documents, body.Query)
	f.variables = append(f.variables, body.Variables)
	f.mu.Unlock()

	// A history page is the same endpoint and the same repository name, so the
	// cursor is what tells the pages apart: page one comes with the repository
	// query, every later page is its own file named after the cursor that asks
	// for it. Flat files, because fixture directories may only be repository
	// names.
	dir := filepath.Join(fixtureRoot, "repos", body.Variables["name"])
	if cursor := body.Variables["cursor"]; cursor != "" {
		f.serve(w, dir, "graphql-history-"+cursor)
		return
	}
	// The file query carries the same variables as the repository query, so
	// only its operation name tells the two apart.
	if strings.Contains(body.Query, "query RepoFiles") {
		f.serve(w, dir, "graphql-files")
		return
	}
	// A repository's Claude files can take more than one query (the plugin
	// manifest decides what else to read), so each is its own file, numbered
	// in the order the collector sends them.
	if strings.Contains(body.Query, "query ClaudeFiles") {
		f.mu.Lock()
		f.claudeQueries[body.Variables["name"]]++
		n := f.claudeQueries[body.Variables["name"]]
		failure := f.claudeFailure
		f.mu.Unlock()
		if failure != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(failure.status)
			if _, err := w.Write([]byte(failure.body)); err != nil {
				f.t.Errorf("write scripted Claude failure: %v", err)
			}
			return
		}
		f.serve(w, dir, "graphql-claude-"+strconv.Itoa(n))
		return
	}
	f.serve(w, dir, "graphql")
}

// handleDiscover pages; the first answer carries an api.github.com Link header
// exactly as GitHub's does (see nextPage in discover.go for why the client
// must re-base it).
func (f *fakeAPI) handleDiscover(w http.ResponseWriter, r *http.Request) {
	page := r.URL.Query().Get("page")
	if page == "" || page == "1" {
		w.Header().Set("Link",
			`<https://api.github.com/user/repos?affiliation=owner&per_page=100&page=2>; rel="next", `+
				`<https://api.github.com/user/repos?affiliation=owner&per_page=100&page=2>; rel="last"`)
		f.serve(w, filepath.Join(fixtureRoot, "discover"), "page-1")
		return
	}
	f.serve(w, filepath.Join(fixtureRoot, "discover"), "page-"+page)
}

// fixtureHandler answers a per-repository endpoint from that repository's
// fixture directory.
func (f *fakeAPI) fixtureHandler(base string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.t.Helper()
		if owner := r.PathValue("owner"); owner != testOwner {
			f.t.Errorf("request for owner %q, want %q", owner, testOwner)
		}
		f.serve(w, filepath.Join(fixtureRoot, "repos", r.PathValue("repo")), base)
	}
}

// handleContents answers the contents API from the repository's fixtures. The
// fixture names are flat — checkFixtureDirNames allows no directory under a
// repository's fixtures that is not itself a fake repository name — so a
// path's slashes become dashes, and a ref is appended after an @. A path with
// no fixture answers 404, as GitHub does for a missing file.
func (f *fakeAPI) handleContents(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	if owner := r.PathValue("owner"); owner != testOwner {
		f.t.Errorf("request for owner %q, want %q", owner, testOwner)
	}
	base := "contents-" + strings.ReplaceAll(r.PathValue("path"), "/", "-")
	if ref := r.URL.Query().Get("ref"); ref != "" {
		base += "@" + ref
	}
	f.serve(w, filepath.Join(fixtureRoot, "repos", r.PathValue("repo")), base)
}

// handleTree answers the git tree API from the repository's git-tree fixture.
// One tree per repository is enough because only the head commit is ever
// listed. The recursive flag is asserted rather than ignored: without it the
// real API lists the root only, and every nested path would read as absent.
func (f *fakeAPI) handleTree(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	if owner := r.PathValue("owner"); owner != testOwner {
		f.t.Errorf("request for owner %q, want %q", owner, testOwner)
	}
	if got := r.URL.Query().Get("recursive"); got != "1" {
		f.t.Errorf("tree request for %s has recursive=%q, want 1", r.URL.Path, got)
	}
	f.serve(w, filepath.Join(fixtureRoot, "repos", r.PathValue("repo")), "git-tree")
}

// handleRuns answers the workflow runs list from the repository's
// actions-runs fixture. The page size is asserted rather than ignored: the
// API's default is thirty, and a commit outside a page that small would read
// as one no workflow ran on.
//
// A request filtered by branch is the default-branch recency read, answered
// from actions-runs-default-branch instead: GitHub filters that list server
// side, so it is a separate recording. Its page size is the maximum too: the
// newest runs on a branch name can all be forks' pull requests, which the
// collector skips. Every fixture repository's default branch is main.
func (f *fakeAPI) handleRuns(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	if owner := r.PathValue("owner"); owner != testOwner {
		f.t.Errorf("request for owner %q, want %q", owner, testOwner)
	}
	dir := filepath.Join(fixtureRoot, "repos", r.PathValue("repo"))
	query := r.URL.Query()
	if query.Has("branch") {
		if got := query.Get("branch"); got != "main" {
			f.t.Errorf("runs request for %s has branch=%q, want main", r.URL.Path, got)
		}
		if got := query.Get("per_page"); got != "100" {
			f.t.Errorf("default-branch runs request for %s has per_page=%q, want 100", r.URL.Path, got)
		}
		f.serve(w, dir, "actions-runs-default-branch")
		return
	}
	if got := query.Get("per_page"); got != "100" {
		f.t.Errorf("runs request for %s has per_page=%q, want 100", r.URL.Path, got)
	}
	f.serve(w, dir, "actions-runs")
}

// handleJobs answers one run's jobs list from actions-jobs-<run id>, flat for
// the same reason the contents fixtures are. A run with no fixture answers
// 404, so a jobs read the collector should not have made fails the run that
// made it. Only the latest attempt is ever asked for: a filter other than the
// default would time a re-run's earlier, abandoned attempts too.
func (f *fakeAPI) handleJobs(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	if owner := r.PathValue("owner"); owner != testOwner {
		f.t.Errorf("request for owner %q, want %q", owner, testOwner)
	}
	query := r.URL.Query()
	if got := query.Get("per_page"); got != "100" {
		f.t.Errorf("jobs request for %s has per_page=%q, want 100", r.URL.Path, got)
	}
	if got := query.Get("filter"); got != "" && got != "latest" {
		f.t.Errorf("jobs request for %s has filter=%q, want the latest attempt", r.URL.Path, got)
	}
	f.serve(w, filepath.Join(fixtureRoot, "repos", r.PathValue("repo")), "actions-jobs-"+r.PathValue("run_id"))
}

// storageListHandler answers the caches or artifacts list from the
// repository's fixture named base. Each fixture is one page, so no Link is
// sent; following one is storage_internal_test.go's to pin. The page size is
// asserted for the reason handleRuns asserts it: the API's default of thirty
// would make every further thirty entries cost a request.
func (f *fakeAPI) storageListHandler(base string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.t.Helper()
		if owner := r.PathValue("owner"); owner != testOwner {
			f.t.Errorf("request for owner %q, want %q", owner, testOwner)
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			f.t.Errorf("%s request has per_page=%q, want 100", r.URL.Path, got)
		}
		f.serve(w, filepath.Join(fixtureRoot, "repos", r.PathValue("repo")), base)
	}
}

// serve writes the fixture named base from dir. A plain <base>.json is a 200;
// <base>.<code>.json carries that status instead, which is how the documented
// non-200 answers — a 404 from private-vulnerability-reporting, a 422 from the
// Actions access endpoint — are recorded alongside the bodies they came with.
func (f *fakeAPI) serve(w http.ResponseWriter, dir, base string) {
	f.t.Helper()
	status := http.StatusOK
	path := filepath.Join(dir, base+".json")

	if _, err := os.Stat(path); err != nil {
		matches, gerr := filepath.Glob(filepath.Join(dir, base+".*.json"))
		if gerr != nil || len(matches) != 1 {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		path = matches[0]
		code := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), base+"."), ".json")
		parsed, perr := strconv.Atoi(code)
		if perr != nil {
			f.t.Errorf("fixture %s: %q is not a status code", path, code)
			return
		}
		status = parsed
	}

	body, err := os.ReadFile(path)
	if err != nil {
		f.t.Errorf("read fixture %s: %v", path, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		f.t.Errorf("write fixture %s: %v", path, err)
	}
}
