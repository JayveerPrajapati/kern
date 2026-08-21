package prprovider

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// RepoInfo holds the owner and name parsed from a git remote URL.
type RepoInfo struct {
	Owner string
	Repo  string
}

var (
	// Matches git@github.com:owner/repo.git
	sshPattern = regexp.MustCompile(`git@github\.com:([^/]+)/([^/]+?)(?:\.git)?$`)
	// Matches https://github.com/owner/repo.git
	httpsPattern = regexp.MustCompile(`https://github\.com/([^/]+)/([^/]+?)(?:\.git)?$`)
)

// DetectRepo runs `git remote get-url origin` in the given directory and parses
// the owner/repo from the GitHub URL. Returns an error if no origin remote is
// found or the URL is not a GitHub URL.
func DetectRepo(dir string) (RepoInfo, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return RepoInfo{}, fmt.Errorf("prprovider: cannot get git remote origin: %w", err)
	}
	url := strings.TrimSpace(string(out))
	return ParseRemoteURL(url)
}

// ParseRemoteURL parses a GitHub remote URL (SSH or HTTPS) into owner/repo.
func ParseRemoteURL(url string) (RepoInfo, error) {
	if m := sshPattern.FindStringSubmatch(url); m != nil {
		return RepoInfo{Owner: m[1], Repo: m[2]}, nil
	}
	if m := httpsPattern.FindStringSubmatch(url); m != nil {
		return RepoInfo{Owner: m[1], Repo: m[2]}, nil
	}
	return RepoInfo{}, fmt.Errorf("prprovider: not a GitHub remote URL: %s", url)
}
