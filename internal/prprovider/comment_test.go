package prprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGitHubProviderCommentPR drives the real HTTP client against a stub
// server and pins the exact request shape: POST to the issues-comments
// endpoint with the bearer token and the JSON body.
func TestGitHubProviderCommentPR(t *testing.T) {
	var gotPath, gotMethod, gotAuth, gotAccept string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	g := &GitHubProvider{client: srv.Client(), token: "ghp_test_token", baseURL: srv.URL}
	body := "## Review findings\n\n- [security/medium] main.go:10 hardcoded credential\n"
	err := g.CommentPR(context.Background(), CommentRequest{
		Owner:  "octocat",
		Repo:   "Hello-World",
		Number: 42,
		Body:   body,
	})
	if err != nil {
		t.Fatalf("CommentPR: %v", err)
	}
	if gotPath != "/repos/octocat/Hello-World/issues/42/comments" {
		t.Errorf("path = %q, want /repos/octocat/Hello-World/issues/42/comments", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotAuth != "Bearer ghp_test_token" {
		t.Errorf("Authorization = %q, want Bearer ghp_test_token", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotBody["body"] != body {
		t.Errorf("body = %q, want %q", gotBody["body"], body)
	}
}

// TestGitHubProviderCommentPRNon2xxTruncated pins the error contract: non-2xx
// fails with the API error body truncated to 500 chars.
func TestGitHubProviderCommentPRNon2xxTruncated(t *testing.T) {
	longMsg := strings.Repeat("x", 2000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintf(w, `{"message":%q}`, longMsg)
	}))
	defer srv.Close()

	g := &GitHubProvider{client: srv.Client(), token: "ghp_test_token", baseURL: srv.URL}
	err := g.CommentPR(context.Background(), CommentRequest{Owner: "o", Repo: "r", Number: 7, Body: "b"})
	if err == nil {
		t.Fatal("expected error on non-2xx")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error should mention the status: %v", err)
	}
	if n := strings.Count(err.Error(), "x"); n > 500 {
		t.Errorf("error body not truncated: %d 'x' chars (want <= 500)", n)
	}
	if n := strings.Count(err.Error(), "x"); n == 0 {
		t.Errorf("error should include the API body: %v", err)
	}
}

// TestGitHubProviderCommentPRValidation pins the input-validation errors.
func TestGitHubProviderCommentPRValidation(t *testing.T) {
	ctx := context.Background()
	emptyToken := &GitHubProvider{token: ""}
	err := emptyToken.CommentPR(ctx, CommentRequest{Owner: "o", Repo: "r", Number: 1, Body: "b"})
	if err == nil {
		t.Error("expected error when token is empty")
	}
	if !strings.Contains(err.Error(), "KERN_GITHUB_TOKEN") {
		t.Errorf("error %q should mention KERN_GITHUB_TOKEN", err)
	}

	withToken := &GitHubProvider{token: "t"}
	if err := withToken.CommentPR(ctx, CommentRequest{Repo: "r", Number: 1, Body: "b"}); err == nil {
		t.Error("expected error when owner/repo empty")
	}
	if err := withToken.CommentPR(ctx, CommentRequest{Owner: "o", Repo: "r", Body: "b"}); err == nil {
		t.Error("expected error when PR number missing")
	}
}

// TestNoopProviderCommentPR pins the no-op success: no error, nothing posted.
func TestNoopProviderCommentPR(t *testing.T) {
	np := NoopProvider{}
	if err := np.CommentPR(context.Background(), CommentRequest{
		Owner: "o", Repo: "r", Number: 3, Body: "b",
	}); err != nil {
		t.Fatalf("NoopProvider.CommentPR: %v", err)
	}
}
