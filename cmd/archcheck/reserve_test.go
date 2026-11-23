package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
)

const (
	testToken  = "test-token-value"
	testCommit = "1111111111111111111111111111111111111111"
)

// tagObject is an annotated tag object that the fake GitHub stores.
type tagObject struct {
	Tag     string `json:"tag"`
	Message string `json:"message"`
	Object  string `json:"object"`
	Type    string `json:"type"`
}

// fakeGitHub serves the endpoints of the GitHub REST API that reserve uses.
// It creates each reference one time, as GitHub does.
type fakeGitHub struct {
	t *testing.T

	mu      sync.Mutex
	refs    map[string]string    // reference name to object SHA
	tags    map[string]tagObject // object SHA to tag object
	hidden  map[string]bool      // references that the listing omits
	refPost int                  // the number of reference creations

	// afterList runs after the listing takes its snapshot, outside the lock.
	afterList func()
	// refFailure, when it returns a status, fails a reference creation.
	refFailure func(ref string) (status int, message string)
	// dropRefResponse closes the connection after a reference creation.
	dropRefResponse bool
	// override, when it handles the request, replaces the fake.
	override func(w http.ResponseWriter, r *http.Request) bool
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	f := &fakeGitHub{t: t, refs: map[string]string{}, tags: map[string]tagObject{}, hidden: map[string]bool{}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, server
}

// addTag adds an annotated reservation tag for number and issue.
func (f *fakeGitHub) addTag(number, issue int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	sha := fmt.Sprintf("%040x", len(f.tags)+1000)
	tag := fmt.Sprintf("design/%04d", number)
	f.tags[sha] = tagObject{Tag: tag, Message: fmt.Sprintf("issue: %d", issue), Object: testCommit, Type: "commit"}
	f.refs["refs/tags/"+tag] = sha
	return sha
}

// posts returns the number of reference creations.
func (f *fakeGitHub) posts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refPost
}

func (f *fakeGitHub) ref(name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sha, ok := f.refs[name]
	return sha, ok
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("X-Github-Api-Version") != apiVersion {
		f.t.Errorf("%s %s: missing authorization or API version", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
		f.t.Errorf("%s %s: Content-Type = %q, want application/json", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
	}
	if f.override != nil && f.override(w, r) {
		return
	}
	const repo = "/repos/o/r"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == repo:
		f.writeJSON(w, http.StatusOK, map[string]string{"default_branch": "main"})
	case r.Method == http.MethodGet && r.URL.Path == repo+"/git/ref/heads/main":
		f.writeJSON(w, http.StatusOK, map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": testCommit}})
	case r.Method == http.MethodGet && r.URL.Path == repo+"/git/matching-refs/tags/design/":
		f.list(w)
	case r.Method == http.MethodPost && r.URL.Path == repo+"/git/tags":
		f.createTag(w, r)
	case r.Method == http.MethodPost && r.URL.Path == repo+"/git/refs":
		f.createRef(w, r)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeGitHub) list(w http.ResponseWriter) {
	f.mu.Lock()
	refs := []map[string]any{}
	for name, sha := range f.refs {
		if !f.hidden[name] {
			refs = append(refs, map[string]any{"ref": name, "object": map[string]string{"sha": sha, "type": "tag"}})
		}
	}
	f.mu.Unlock()
	if f.afterList != nil {
		f.afterList()
	}
	f.writeJSON(w, http.StatusOK, refs)
}

func (f *fakeGitHub) createTag(w http.ResponseWriter, r *http.Request) {
	var tag tagObject
	if err := json.NewDecoder(r.Body).Decode(&tag); err != nil {
		f.t.Errorf("decode the tag object: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sha := fmt.Sprintf("%040x", len(f.tags)+1)
	f.tags[sha] = tag
	f.writeJSON(w, http.StatusCreated, map[string]string{"sha": sha})
}

func (f *fakeGitHub) createRef(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("decode the reference: %v", err)
	}
	f.mu.Lock()
	f.refPost++
	if f.refFailure != nil {
		if status, message := f.refFailure(body.Ref); status != 0 {
			f.mu.Unlock()
			f.writeJSON(w, status, map[string]string{"message": message})
			return
		}
	}
	if _, ok := f.refs[body.Ref]; ok {
		f.mu.Unlock()
		f.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "Reference already exists"})
		return
	}
	f.refs[body.Ref] = body.SHA
	f.mu.Unlock()
	if f.dropRefResponse {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			f.t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
		return
	}
	f.writeJSON(w, http.StatusCreated, map[string]any{"ref": body.Ref, "object": map[string]string{"sha": body.SHA}})
}

func (f *fakeGitHub) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		f.t.Errorf("encode the response: %v", err)
	}
}

// records returns a file system with design records for numbers.
func records(numbers ...int) fstest.MapFS {
	fsys := fstest.MapFS{}
	for _, n := range numbers {
		fsys[fmt.Sprintf("architecture/designs/%04d-r.md", n)] = &fstest.MapFile{Data: fmt.Appendf(nil, "---\ndesign: %d\nissue: 1\ntitle: R\n---\n", n)}
	}
	return fsys
}

func testEnv(api string) environment {
	return environment{api: api, getenv: func(key string) string {
		if key == "GH_TOKEN" {
			return testToken
		}
		return ""
	}}
}

func runReserveCommand(ctx context.Context, env environment, fsys fstest.MapFS, args ...string) (status int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	status = run(ctx, append([]string{"reserve"}, args...), fsys, ".", env, &out, &errOut)
	return status, out.String(), errOut.String()
}

func TestReserveNextNumber(t *testing.T) {
	f, server := newFakeGitHub(t)
	existing := f.addTag(3, 7)
	status, stdout, stderr := runReserveCommand(t.Context(), testEnv(server.URL), records(1, 2), "-repo", "o/r", "-issue", "38")
	if status != 0 || stdout != "0004\n" || stderr != "" {
		t.Fatalf("reserve = %d, %q, %q, want 0 and 0004", status, stdout, stderr)
	}
	sha, ok := f.ref("refs/tags/design/0004")
	if !ok {
		t.Fatal("reserve created no reference refs/tags/design/0004")
	}
	want := tagObject{Tag: "design/0004", Message: "issue: 38", Object: testCommit, Type: "commit"}
	if diff := cmp.Diff(want, f.tags[sha]); diff != "" {
		t.Errorf("tag object mismatch (-want +got):\n%s", diff)
	}
	if got, _ := f.ref("refs/tags/design/0003"); got != existing {
		t.Errorf("refs/tags/design/0003 = %s, want the unchanged %s", got, existing)
	}
}

func TestReserveFirstNumber(t *testing.T) {
	_, server := newFakeGitHub(t)
	status, stdout, stderr := runReserveCommand(t.Context(), testEnv(server.URL), fstest.MapFS{}, "-repo", "o/r", "-issue", "1", "-designs", "none")
	if status != 0 || stdout != "0001\n" {
		t.Errorf("reserve = %d, %q, %q, want 0 and 0001", status, stdout, stderr)
	}
}

func TestReserveConcurrent(t *testing.T) {
	f, server := newFakeGitHub(t)
	// Each listing takes its snapshot, and then waits until the other listing
	// has its snapshot too, so both calls select the same first number.
	var listed sync.WaitGroup
	listed.Add(2)
	f.afterList = func() {
		listed.Done()
		listed.Wait()
	}
	var wg sync.WaitGroup
	results := make([]string, 2)
	for i := range results {
		wg.Go(func() {
			status, stdout, stderr := runReserveCommand(t.Context(), testEnv(server.URL), records(1), "-repo", "o/r", "-issue", strconv.Itoa(40+i))
			if status != 0 {
				t.Errorf("reserve %d = %d, %q", i, status, stderr)
			}
			results[i] = stdout
		})
	}
	wg.Wait()
	if results[0] == results[1] {
		t.Fatalf("two concurrent reserve calls both got %q", results[0])
	}
	got := map[string]bool{results[0]: true, results[1]: true}
	if !got["0002\n"] || !got["0003\n"] || f.posts() != 3 {
		t.Errorf("reserve results = %q after %d reference creations, want 0002 and 0003 after 3", results, f.posts())
	}
}

func TestReserveSkipsExistingTag(t *testing.T) {
	f, server := newFakeGitHub(t)
	// Another caller created design/0002 after the listing.
	existing := f.addTag(2, 9)
	f.hidden["refs/tags/design/0002"] = true
	status, stdout, stderr := runReserveCommand(t.Context(), testEnv(server.URL), records(1), "-repo", "o/r", "-issue", "38")
	if status != 0 || stdout != "0003\n" {
		t.Fatalf("reserve = %d, %q, %q, want 0 and 0003", status, stdout, stderr)
	}
	if got, _ := f.ref("refs/tags/design/0002"); got != existing {
		t.Errorf("refs/tags/design/0002 = %s, want the unchanged %s", got, existing)
	}
	if f.tags[existing].Message != "issue: 9" {
		t.Errorf("the tag object of design/0002 changed to %+v", f.tags[existing])
	}
}

func TestReserveLostResponse(t *testing.T) {
	// The reference exists and points to the tag object of this call, but
	// another call can have created an identical tag object with the same
	// SHA. So a lost response leaves the outcome unknown.
	f, server := newFakeGitHub(t)
	f.dropRefResponse = true
	status, stdout, stderr := runReserveCommand(t.Context(), testEnv(server.URL), records(1), "-repo", "o/r", "-issue", "38")
	if status != 1 || stdout != "" || !strings.Contains(stderr, "archcheck: create design/0002: the outcome is unknown, so do not use the number: ") {
		t.Errorf("reserve = %d, %q, %q, want 1 and an unknown outcome", status, stdout, stderr)
	}
	if f.posts() != 1 {
		t.Errorf("reserve made %d reference creations, want 1", f.posts())
	}
}

func TestReserveFailures(t *testing.T) {
	bigBody := strings.Repeat("x", maxResponse+1)
	tests := []struct {
		name     string
		setup    func(f *fakeGitHub)
		args     []string
		stderr   string
		refPosts int
	}{
		{
			name:     "other 422",
			setup:    func(f *fakeGitHub) { f.refFailure = func(string) (int, string) { return 422, "Object does not exist" } },
			stderr:   `archcheck: create design/0002: GitHub returned 422 "Object does not exist"`,
			refPosts: 1,
		},
		{
			name:     "server error",
			setup:    func(f *fakeGitHub) { f.refFailure = func(string) (int, string) { return 500, "" } },
			stderr:   `create design/0002: GitHub returned 500 ""`,
			refPosts: 1,
		},
		{
			name: "tag object failure",
			setup: func(f *fakeGitHub) {
				f.override = respond(http.MethodPost, "/repos/o/r/git/tags", http.StatusForbidden, `{"message":"Resource not accessible"}`)
			},
			stderr: `create the tag object of design/0002: GitHub returned 403 "Resource not accessible"`,
		},
		{
			name: "tag object without SHA",
			setup: func(f *fakeGitHub) {
				f.override = respond(http.MethodPost, "/repos/o/r/git/tags", http.StatusCreated, `{"sha":"x"}`)
			},
			stderr: "create the tag object of design/0002: GitHub returned no SHA",
		},
		{
			name: "repository failure",
			setup: func(f *fakeGitHub) {
				f.override = respond(http.MethodGet, "/repos/o/r", http.StatusNotFound, `{"message":"Not Found"}`)
			},
			stderr: `read the repository: GitHub returned 404 "Not Found"`,
		},
		{
			name:   "redirect",
			setup:  func(f *fakeGitHub) { f.override = redirect("/repos/o/r") },
			stderr: "read the repository: GitHub returned 301",
		},
		{
			name:   "large response",
			setup:  func(f *fakeGitHub) { f.override = respond(http.MethodGet, "/repos/o/r", http.StatusOK, bigBody) },
			stderr: "read the repository: the response is larger than 8 MiB",
		},
		{
			name:   "invalid JSON",
			setup:  func(f *fakeGitHub) { f.override = respond(http.MethodGet, "/repos/o/r", http.StatusOK, "{") },
			stderr: "read the repository: decode the response",
		},
		{
			name: "default branch failure",
			setup: func(f *fakeGitHub) {
				f.override = respond(http.MethodGet, "/repos/o/r/git/ref/heads/main", http.StatusConflict, `{"message":"Git Repository is empty."}`)
			},
			stderr: `read the default branch: GitHub returned 409 "Git Repository is empty."`,
		},
		{
			name: "default branch without SHA",
			setup: func(f *fakeGitHub) {
				f.override = respond(http.MethodGet, "/repos/o/r/git/ref/heads/main", http.StatusOK, `{"object":{"sha":"abc"}}`)
			},
			stderr: "read the default branch: GitHub returned no commit SHA",
		},
		{
			name: "listing failure",
			setup: func(f *fakeGitHub) {
				f.override = respond(http.MethodGet, "/repos/o/r/git/matching-refs/tags/design/", http.StatusInternalServerError, "")
			},
			stderr: "list the reservation tags: GitHub returned 500",
		},
		{
			name: "status 300",
			setup: func(f *fakeGitHub) {
				f.override = respond(http.MethodGet, "/repos/o/r", http.StatusMultipleChoices, `{"message":"Multiple Choices"}`)
			},
			stderr: `read the repository: GitHub returned 300 "Multiple Choices"`,
		},
		{
			name:   "no free number",
			setup:  func(f *fakeGitHub) { f.addTag(9999, 1) },
			stderr: "archcheck: no design number up to 9999 is free",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, server := newFakeGitHub(t)
			tc.setup(f)
			status, stdout, stderr := runReserveCommand(t.Context(), testEnv(server.URL), records(1), "-repo", "o/r", "-issue", "38")
			if status != 1 || stdout != "" || !strings.Contains(stderr, tc.stderr) {
				t.Errorf("reserve = %d, %q, %q, want 1 and %q", status, stdout, stderr, tc.stderr)
			}
			if strings.Contains(stderr, testToken) {
				t.Errorf("stderr %q contains the token", stderr)
			}
			if f.posts() != tc.refPosts {
				t.Errorf("reserve made %d reference creations, want %d", f.posts(), tc.refPosts)
			}
		})
	}
}

// respond returns an override that answers one request with a fixed status
// and body.
func respond(method, path string, status int, body string) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != method || r.URL.Path != path {
			return false
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		return true
	}
}

// redirect returns an override that redirects one path to another host.
func redirect(path string) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != path {
			return false
		}
		http.Redirect(w, r, "https://attacker.example/steal", http.StatusMovedPermanently)
		return true
	}
}

func TestReserveLastNumber(t *testing.T) {
	f, server := newFakeGitHub(t)
	f.addTag(9998, 1)
	status, stdout, stderr := runReserveCommand(t.Context(), testEnv(server.URL), records(1), "-repo", "o/r", "-issue", "38")
	if status != 0 || stdout != "9999\n" {
		t.Errorf("reserve = %d, %q, %q, want 0 and 9999", status, stdout, stderr)
	}
}

func TestReserveLargestResponse(t *testing.T) {
	f, server := newFakeGitHub(t)
	body := `{"default_branch":"main"}`
	body += strings.Repeat(" ", maxResponse-len(body))
	f.override = respond(http.MethodGet, "/repos/o/r", http.StatusOK, body)
	status, stdout, stderr := runReserveCommand(t.Context(), testEnv(server.URL), records(1), "-repo", "o/r", "-issue", "38")
	if status != 0 || stdout != "0002\n" {
		t.Errorf("reserve = %d, %q, %q, want 0 and 0002 with a response of exactly 8 MiB", status, stdout, stderr)
	}
}

func TestReserveUsage(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		stderr string
	}{
		{name: "no repository", args: []string{"-issue", "1"}, stderr: "usage: archcheck reserve"},
		{name: "bad repository", args: []string{"-repo", "o/r/x", "-issue", "1"}, stderr: "usage: archcheck reserve"},
		{name: "dot repository", args: []string{"-repo", "o/..", "-issue", "1"}, stderr: "usage: archcheck reserve"},
		{name: "no issue", args: []string{"-repo", "o/r"}, stderr: "usage: archcheck reserve"},
		{name: "negative issue", args: []string{"-repo", "o/r", "-issue", "-1"}, stderr: "usage: archcheck reserve"},
		{name: "argument", args: []string{"-repo", "o/r", "-issue", "1", "extra"}, stderr: "usage: archcheck reserve"},
		{name: "unknown flag", args: []string{"-unknown"}, stderr: "flag provided but not defined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, _, stderr := runReserveCommand(t.Context(), testEnv("http://unused.invalid"), fstest.MapFS{}, tc.args...)
			if status != 2 || !strings.Contains(stderr, tc.stderr) {
				t.Errorf("reserve = %d, %q, want 2 and %q", status, stderr, tc.stderr)
			}
		})
	}
}

func TestReserveNoToken(t *testing.T) {
	env := environment{api: "http://unused.invalid", getenv: func(string) string { return "" }}
	status, _, stderr := runReserveCommand(t.Context(), env, fstest.MapFS{}, "-repo", "o/r", "-issue", "1")
	if status != 1 || !strings.Contains(stderr, "archcheck: GH_TOKEN must hold a GitHub token") {
		t.Errorf("reserve = %d, %q, want 1 and a missing token", status, stderr)
	}
}

func TestReserveUnreadableDesigns(t *testing.T) {
	fsys := fstest.MapFS{"architecture/designs": {Data: []byte("a file, not a directory")}}
	status, _, stderr := runReserveCommand(t.Context(), testEnv("http://unused.invalid"), fsys, "-repo", "o/r", "-issue", "1")
	if status != 1 || !strings.Contains(stderr, "list the design records") {
		t.Errorf("reserve = %d, %q, want 1 and a list error", status, stderr)
	}
}

func TestReserveCanceled(t *testing.T) {
	_, server := newFakeGitHub(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	status, _, stderr := runReserveCommand(ctx, testEnv(server.URL), records(1), "-repo", "o/r", "-issue", "1")
	if status != 1 || !strings.Contains(stderr, "context canceled") {
		t.Errorf("reserve = %d, %q, want 1 and a canceled context", status, stderr)
	}
}

// failWriter fails each write.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestReserveWriteFailure(t *testing.T) {
	f, server := newFakeGitHub(t)
	var errOut bytes.Buffer
	status := run(t.Context(), []string{"reserve", "-repo", "o/r", "-issue", "38"}, records(1), ".", testEnv(server.URL), failWriter{}, &errOut)
	if status != 1 || !strings.Contains(errOut.String(), "archcheck: design/0002 is reserved, but the number was not written: disk full") {
		t.Errorf("reserve = %d, %q, want 1 and a write failure", status, errOut.String())
	}
	if f.posts() != 1 {
		t.Errorf("reserve made %d reference creations, want 1", f.posts())
	}
}

func TestEscapePath(t *testing.T) {
	if got := escapePath("feature/a b#c"); got != "feature/a%20b%23c" {
		t.Errorf("escapePath() = %q, want feature/a%%20b%%23c", got)
	}
}

func TestNewClientTimeout(t *testing.T) {
	if got := newClient().Timeout; got != 30*time.Second {
		t.Errorf("newClient().Timeout = %v, want 30s, so a request cannot hang", got)
	}
}
