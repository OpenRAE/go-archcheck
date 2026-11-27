package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/OpenRAE/go-archcheck/internal/record"
)

const (
	// gitHubAPI is the base URL of the GitHub REST API. The token goes to no
	// other host.
	gitHubAPI = "https://api.github.com"
	// apiVersion is the GitHub REST API version of the requests.
	apiVersion = "2026-03-10"
	// maxResponse limits the size of each response body.
	maxResponse = 8 << 20
	// maxDesign is the largest design number that NNNN-slug.md can hold.
	maxDesign = 9999
	// referenceExists is the GitHub message for a reference that exists.
	referenceExists = "Reference already exists"
	reserveUsage    = "usage: archcheck reserve -repo owner/name -issue N [-designs dir]"
)

var (
	repoPattern = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	shaPattern  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

// runReserve reserves the next free design number for an issue, prints it
// and returns the exit status.
func runReserve(ctx context.Context, args []string, fsys fs.FS, env environment, stdout, stderr io.Writer) int {
	var repo, designs string
	var issue int
	flags := flag.NewFlagSet("archcheck reserve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&repo, "repo", "", "the GitHub repository, as owner/name")
	flags.IntVar(&issue, "issue", 0, "the issue of the design record")
	flags.StringVar(&designs, "designs", "architecture/designs", "the directory of the design records")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || !validRepo(repo) || issue <= 0 {
		_, _ = fmt.Fprintln(stderr, reserveUsage)
		return 2
	}
	number, err := reserveFor(ctx, fsys, designs, env, repo, issue)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "archcheck: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "%04d\n", number); err != nil {
		_, _ = fmt.Fprintf(stderr, "archcheck: %s is reserved, but the number was not written: %v\n", record.ReservationTag(number), err)
		return 1
	}
	return 0
}

// validRepo reports whether repo is owner/name, with a name that is not only
// dots.
func validRepo(repo string) bool {
	_, name, _ := strings.Cut(repo, "/")
	return repoPattern.MatchString(repo) && strings.Trim(name, ".") != ""
}

// reserveFor reserves the next free design number of repo for issue.
func reserveFor(ctx context.Context, fsys fs.FS, designs string, env environment, repo string, issue int) (int, error) {
	token := env.getenv("GH_TOKEN")
	if token == "" {
		return 0, errors.New("GH_TOKEN must hold a GitHub token with write access to the repository contents")
	}
	names, err := record.Names(fsys, designs)
	if err != nil {
		return 0, err
	}
	g := &gitHub{client: newClient(), repo: env.api + "/repos/" + repo, token: token}
	return g.reserve(ctx, names, issue)
}

// newClient returns an HTTP client with a time limit that follows no
// redirects, so the token never goes to another URL.
func newClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// gitHub calls the GitHub REST API for one repository.
type gitHub struct {
	client *http.Client
	repo   string // the API URL of the repository
	token  string
}

// gitRef is a Git reference of the GitHub API.
type gitRef struct {
	Ref    string `json:"ref"`
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

// reserve reserves the next free design number for issue. The next free
// number is one more than the largest number of the records in names and of
// the reservation tags. When another caller creates that tag first, reserve
// tries the next number.
func (g *gitHub) reserve(ctx context.Context, names []string, issue int) (int, error) {
	commit, err := g.defaultHead(ctx)
	if err != nil {
		return 0, err
	}
	highest, err := g.highestReservation(ctx)
	if err != nil {
		return 0, err
	}
	for _, name := range names {
		if n, ok := record.Number(name); ok {
			highest = max(highest, n)
		}
	}
	for number := highest + 1; number <= maxDesign; number++ {
		reserved, err := g.reserveNumber(ctx, number, issue, commit)
		if err != nil {
			return 0, err
		}
		if reserved {
			return number, nil
		}
	}
	return 0, fmt.Errorf("no design number up to %d is free", maxDesign)
}

// defaultHead returns the SHA of the latest commit of the default branch.
func (g *gitHub) defaultHead(ctx context.Context) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.call(ctx, http.MethodGet, "", nil, &repo); err != nil {
		return "", fmt.Errorf("read the repository: %w", err)
	}
	var ref gitRef
	if err := g.call(ctx, http.MethodGet, "/git/ref/heads/"+escapePath(repo.DefaultBranch), nil, &ref); err != nil {
		return "", fmt.Errorf("read the default branch: %w", err)
	}
	if !shaPattern.MatchString(ref.Object.SHA) {
		return "", errors.New("read the default branch: GitHub returned no commit SHA")
	}
	return ref.Object.SHA, nil
}

// highestReservation returns the largest number of the reservation tags, or
// 0 when there is none.
func (g *gitHub) highestReservation(ctx context.Context) (int, error) {
	var refs []gitRef
	if err := g.call(ctx, http.MethodGet, "/git/matching-refs/tags/design/", nil, &refs); err != nil {
		return 0, fmt.Errorf("list the reservation tags: %w", err)
	}
	highest := 0
	for _, ref := range refs {
		if n, ok := record.ReservedNumber(ref.Ref); ok {
			highest = max(highest, n)
		}
	}
	return highest, nil
}

// reserveNumber creates the reservation tag of number for issue on commit.
// It returns false when the tag exists. Only the creation of the reference
// reserves the number; the tag object alone does not.
func (g *gitHub) reserveNumber(ctx context.Context, number, issue int, commit string) (bool, error) {
	tag := record.ReservationTag(number)
	var object struct {
		SHA string `json:"sha"`
	}
	err := g.call(ctx, http.MethodPost, "/git/tags", map[string]string{
		"tag": tag, "message": record.ReservationMessage(issue), "object": commit, "type": "commit",
	}, &object)
	if err != nil {
		return false, fmt.Errorf("create the tag object of %s: %w", tag, err)
	}
	if !shaPattern.MatchString(object.SHA) {
		return false, fmt.Errorf("create the tag object of %s: GitHub returned no SHA", tag)
	}
	err = g.call(ctx, http.MethodPost, "/git/refs", map[string]string{"ref": "refs/tags/" + tag, "sha": object.SHA}, nil)
	var status *statusError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &status):
		if status.code == http.StatusUnprocessableEntity && status.message == referenceExists {
			return false, nil
		}
		return false, fmt.Errorf("create %s: %w", tag, err)
	}
	// With no response, the reference can exist. Two callers can create
	// identical tag objects, so the SHA of the reference cannot show which
	// caller created it.
	return false, fmt.Errorf("create %s: the outcome is unknown, so do not use the number: %w", tag, err)
}

// statusError is a response with a status outside 2xx.
type statusError struct {
	code    int
	message string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("GitHub returned %d %q", e.code, e.message)
}

// call sends a request with the JSON body in, when it is not nil, and
// decodes the JSON response into out, when it is not nil.
func (g *gitHub) call(ctx context.Context, method, path string, in map[string]string, out any) error {
	var body io.Reader = http.NoBody
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.repo+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("X-Github-Api-Version", apiVersion)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("read the response: %w", err)
	}
	if len(data) > maxResponse {
		return errors.New("the response is larger than 8 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		return &statusError{code: resp.StatusCode, message: e.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode the response: %w", err)
	}
	return nil
}

// escapePath escapes each segment of a slash-separated name for a URL path.
func escapePath(name string) string {
	segments := strings.Split(name, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}
