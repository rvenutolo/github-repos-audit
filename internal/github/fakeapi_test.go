package github_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
type call struct {
	method string
	path   string
}

// scriptedFailure is one answer the fake gives instead of a fixture. It exists
// so a test can put GitHub's transient failures — a secondary rate limit, a
// bad gateway — in front of an endpoint that otherwise answers normally.
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
	failures  map[string][]scriptedFailure
}

// newFakeAPI starts a server backed by testdata/api and stops it with the test.
func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()

	f := &fakeAPI{t: t, failures: map[string][]scriptedFailure{}}
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

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r.Method, r.URL.Path)
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

func (f *fakeAPI) record(method, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{method: method, path: path})
}

// failNext queues answers for the next calls to path, in the order given,
// after which that path serves its fixture again.
func (f *fakeAPI) failNext(path string, answers ...scriptedFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[path] = append(f.failures[path], answers...)
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

// handleGraphQL answers with the recorded response for the repository named in
// the request's variables, which is how one endpoint serves every repository.
func (f *fakeAPI) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	var body struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("decode graphql request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.documents = append(f.documents, body.Query)
	f.mu.Unlock()

	f.serve(w, filepath.Join(fixtureRoot, "repos", body.Variables["name"]), "graphql")
}

// handleDiscover pages: the first answer carries a Link header naming
// api.github.com, exactly as GitHub's does, so the client is forced to re-base
// it onto this server rather than following the host it was given.
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
