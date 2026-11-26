// Package gh is a small GitHub REST + GraphQL client covering what the
// reviewer needs. Standard library only.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrNotFound is returned for 404 responses.
var ErrNotFound = errors.New("not found")

// APIError is a non-2xx response.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Body) }

// Client talks to one repository.
type Client struct {
	Token      string
	APIURL     string // https://api.github.com
	GraphQLURL string // https://api.github.com/graphql
	Owner      string
	Repo       string
	HTTP       *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) do(ctx context.Context, method, path, accept string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(buf)
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = strings.TrimRight(c.APIURL, "/") + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if accept == "" {
		accept = "application/vnd.github+json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Body: string(data)}
	}
	return data, nil
}

func (c *Client) rest(ctx context.Context, method, path string, body, out any) error {
	data, err := c.do(ctx, method, path, "", body)
	if err != nil {
		return err
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *Client) repoPath() string {
	return "/repos/" + url.PathEscape(c.Owner) + "/" + url.PathEscape(c.Repo)
}

// PR is a pull request.
type PR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	Head   struct {
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"base"`
}

// File is a changed file in a PR or comparison.
type File struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Patch            string `json:"patch"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
}

// GetPR fetches a pull request.
func (c *Client) GetPR(ctx context.Context, n int) (*PR, error) {
	var pr PR
	return &pr, c.rest(ctx, "GET", fmt.Sprintf("%s/pulls/%d", c.repoPath(), n), nil, &pr)
}

// ListPRFiles lists all changed files in a PR (GitHub caps this at 3000).
func (c *Client) ListPRFiles(ctx context.Context, n int) ([]File, error) {
	var all []File
	for page := 1; page <= 30; page++ {
		var batch []File
		if err := c.rest(ctx, "GET", fmt.Sprintf("%s/pulls/%d/files?per_page=100&page=%d", c.repoPath(), n, page), nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// Comparison is the result of comparing two commits.
type Comparison struct {
	Status string `json:"status"` // ahead, behind, identical, diverged
	Files  []File `json:"files"`
}

// Compare compares base...head.
func (c *Client) Compare(ctx context.Context, base, head string) (*Comparison, error) {
	var cmp Comparison
	return &cmp, c.rest(ctx, "GET", fmt.Sprintf("%s/compare/%s...%s?per_page=100", c.repoPath(), url.PathEscape(base), url.PathEscape(head)), nil, &cmp)
}

// GetContent returns the raw content of a file at ref.
func (c *Client) GetContent(ctx context.Context, path, ref string) (string, error) {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	data, err := c.do(ctx, "GET", fmt.Sprintf("%s/contents/%s?ref=%s", c.repoPath(), strings.Join(segs, "/"), url.QueryEscape(ref)), "application/vnd.github.raw+json", nil)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// IssueComment is a top-level PR conversation comment.
type IssueComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// ListIssueComments lists conversation comments on a PR.
func (c *Client) ListIssueComments(ctx context.Context, n int) ([]IssueComment, error) {
	var all []IssueComment
	for page := 1; page <= 20; page++ {
		var batch []IssueComment
		if err := c.rest(ctx, "GET", fmt.Sprintf("%s/issues/%d/comments?per_page=100&page=%d", c.repoPath(), n, page), nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// CreateIssueComment posts a conversation comment.
func (c *Client) CreateIssueComment(ctx context.Context, n int, body string) error {
	return c.rest(ctx, "POST", fmt.Sprintf("%s/issues/%d/comments", c.repoPath(), n), map[string]string{"body": body}, nil)
}

// UpdateIssueComment edits a conversation comment.
func (c *Client) UpdateIssueComment(ctx context.Context, id int64, body string) error {
	return c.rest(ctx, "PATCH", fmt.Sprintf("%s/issues/comments/%d", c.repoPath(), id), map[string]string{"body": body}, nil)
}

// ReviewComment is an inline comment in a review.
type ReviewComment struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Line      int    `json:"line"`
	Side      string `json:"side"`
	StartLine int    `json:"start_line,omitempty"`
	StartSide string `json:"start_side,omitempty"`
}

// CreateReview posts a review with inline comments.
func (c *Client) CreateReview(ctx context.Context, n int, commitID, body string, comments []ReviewComment) error {
	req := map[string]any{
		"commit_id": commitID,
		"body":      body,
		"event":     "COMMENT",
		"comments":  comments,
	}
	return c.rest(ctx, "POST", fmt.Sprintf("%s/pulls/%d/reviews", c.repoPath(), n), req, nil)
}

// ---- GraphQL ----

func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	data, err := c.do(ctx, "POST", c.GraphQLURL, "application/json", map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, len(env.Errors))
		for i, e := range env.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("github graphql: %s", strings.Join(msgs, "; "))
	}
	if out != nil {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// Thread is a PR review thread with its first comment.
type Thread struct {
	ID           string
	IsResolved   bool
	IsOutdated   bool
	Path         string
	Line         int // current line on the head commit, 0 if outdated
	OriginalLine int
	FirstBody    string
	FirstAuthor  string
}

const threadsQuery = `query($owner:String!,$repo:String!,$n:Int!,$after:String){
  repository(owner:$owner,name:$repo){
    pullRequest(number:$n){
      reviewThreads(first:100,after:$after){
        pageInfo{hasNextPage endCursor}
        nodes{
          id isResolved isOutdated path line originalLine
          comments(first:1){nodes{body author{login}}}
        }
      }
    }
  }
}`

// ReviewThreads lists all review threads on a PR.
func (c *Client) ReviewThreads(ctx context.Context, n int) ([]Thread, error) {
	var out []Thread
	var after any
	for i := 0; i < 50; i++ {
		var resp struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							ID           string `json:"id"`
							IsResolved   bool   `json:"isResolved"`
							IsOutdated   bool   `json:"isOutdated"`
							Path         string `json:"path"`
							Line         *int   `json:"line"`
							OriginalLine *int   `json:"originalLine"`
							Comments     struct {
								Nodes []struct {
									Body   string `json:"body"`
									Author *struct {
										Login string `json:"login"`
									} `json:"author"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		vars := map[string]any{"owner": c.Owner, "repo": c.Repo, "n": n, "after": after}
		if err := c.graphql(ctx, threadsQuery, vars, &resp); err != nil {
			return nil, err
		}
		rt := resp.Repository.PullRequest.ReviewThreads
		for _, nd := range rt.Nodes {
			t := Thread{ID: nd.ID, IsResolved: nd.IsResolved, IsOutdated: nd.IsOutdated, Path: nd.Path}
			if nd.Line != nil {
				t.Line = *nd.Line
			}
			if nd.OriginalLine != nil {
				t.OriginalLine = *nd.OriginalLine
			}
			if len(nd.Comments.Nodes) > 0 {
				t.FirstBody = nd.Comments.Nodes[0].Body
				if a := nd.Comments.Nodes[0].Author; a != nil {
					t.FirstAuthor = a.Login
				}
			}
			out = append(out, t)
		}
		if !rt.PageInfo.HasNextPage {
			break
		}
		after = rt.PageInfo.EndCursor
	}
	return out, nil
}

// ReplyToThread adds a reply to a review thread.
func (c *Client) ReplyToThread(ctx context.Context, threadID, body string) error {
	const q = `mutation($id:ID!,$body:String!){addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId:$id,body:$body}){comment{id}}}`
	return c.graphql(ctx, q, map[string]any{"id": threadID, "body": body}, nil)
}

// ResolveThread marks a review thread as resolved.
func (c *Client) ResolveThread(ctx context.Context, threadID string) error {
	const q = `mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{isResolved}}}`
	return c.graphql(ctx, q, map[string]any{"id": threadID}, nil)
}
