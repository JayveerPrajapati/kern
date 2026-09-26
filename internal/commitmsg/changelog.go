// Changelog support for kern commitmsg --changelog <range>.
//
// The release-notes draft is fully deterministic: it parses the conventional
// commit subjects of the given git range and groups them by subsystem (the
// commit's scope) and type. No LLM, no network, no new dependencies — only
// the git CLI (like the rest of the package's git-facing surface) and stdlib.
package commitmsg

import (
	"bufio"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// maxChangelogCommits caps how many commits the draft will process. git is
// told to stop after this many commits (-n) and the stream reader enforces the
// same bound, so a huge range can never balloon memory or output.
const maxChangelogCommits = 2000

// changelogTypes is the fixed order of type sections within a subsystem.
// "other" collects commits that are not conventional or use an unknown type.
var changelogTypes = []string{
	"feat", "fix", "refactor", "perf", "docs", "test",
	"build", "ci", "chore", "style", "revert", "other",
}

// changelogTypeSet is the membership set for changelogTypes.
var changelogTypeSet = func() map[string]bool {
	m := make(map[string]bool, len(changelogTypes))
	for _, t := range changelogTypes {
		m[t] = true
	}
	return m
}()

// changelogCommit is one parsed `git log --oneline` entry.
type changelogCommit struct {
	Hash    string // short hash (git log --oneline short form)
	Subject string // conventional subject (type(scope) prefix stripped)
	Type    string // conventional type, or "other"
	Scope   string // conventional scope, or "other" when empty
}

// Changelog renders a subsystem-grouped release-notes draft for the given git
// revision range in root. Sections are ordered by subsystem name (sorted);
// types within a subsystem follow the fixed changelogTypes order; each commit
// renders as "- <subject> (<short-hash>)". The draft header carries the range
// and the commit count.
//
// An empty range, a range that matches no commits, or a git failure (not a
// repository, unknown revision) returns a clear error — callers surface it as
// a fatal exit-1.
func Changelog(root, rng string) (string, error) {
	if strings.TrimSpace(rng) == "" {
		return "", errors.New("changelog: empty revision range (usage: --changelog <range>, e.g. HEAD~5..HEAD)")
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	commits, err := changelogCommits(root, rng)
	if err != nil {
		return "", err
	}
	if len(commits) == 0 {
		return "", fmt.Errorf("changelog: no commits in range %s", rng)
	}
	return renderChangelog(rng, commits), nil
}

// changelogCommits runs `git log --oneline -n <max> <range>` in root and
// parses the stream. The -n argument makes git itself stop at the cap, so the
// piped stdout is bounded even for enormous ranges.
func changelogCommits(root, rng string) ([]changelogCommit, error) {
	cmd := exec.Command("git", "-C", root, "log", "--oneline", "-n", fmt.Sprintf("%d", maxChangelogCommits), rng)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("changelog: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("changelog: git log %s: %v", rng, err)
	}
	var commits []changelogCommit
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() && len(commits) < maxChangelogCommits {
		if c, ok := parseChangelogLine(sc.Text()); ok {
			commits = append(commits, c)
		}
	}
	if err := sc.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("changelog: read git log: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("changelog: git log %s: %s", rng, msg)
	}
	return commits, nil
}

// parseChangelogLine parses one `git log --oneline` line ("<short-hash>
// <subject>"). Conventional subjects are split into type/scope/subject; a
// subject whose type is unknown or that is not conventional at all falls into
// the "other" type under the "other" subsystem.
func parseChangelogLine(line string) (changelogCommit, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return changelogCommit{}, false
	}
	hash, rest, ok := strings.Cut(line, " ")
	if !ok {
		return changelogCommit{}, false
	}
	hash = strings.TrimSpace(hash)
	rest = strings.TrimSpace(rest)
	if hash == "" || rest == "" {
		return changelogCommit{}, false
	}
	c := changelogCommit{Hash: hash, Subject: rest}
	if typ, scope, subject, ok := parseConventionalSubject(rest); ok {
		c.Type = typ
		c.Scope = scope
		c.Subject = subject
		if c.Scope == "" {
			c.Scope = "other" // empty scope → "other" subsystem
		}
	} else {
		c.Type = "other"
		c.Scope = "other"
	}
	return c, true
}

// parseConventionalSubject parses "type(scope): subject" (scope optional).
// It only accepts the known conventional types; anything else is reported as
// not-conventional so the caller buckets it under "other".
func parseConventionalSubject(s string) (typ, scope, subject string, ok bool) {
	colon := strings.Index(s, ":")
	if colon <= 0 {
		return "", "", "", false
	}
	head := strings.TrimSpace(s[:colon])
	subject = strings.TrimSpace(s[colon+1:])
	if head == "" || subject == "" {
		return "", "", "", false
	}
	if i := strings.Index(head, "("); i > 0 {
		if !strings.HasSuffix(head, ")") {
			return "", "", "", false
		}
		typ = strings.TrimSpace(head[:i])
		scope = strings.TrimSpace(head[i+1 : len(head)-1])
	} else {
		typ = strings.TrimSpace(head)
	}
	if !changelogTypeSet[typ] {
		return "", "", "", false
	}
	return typ, scope, subject, true
}

// renderChangelog renders the draft: header (range + count), then subsystems
// sorted by name, then the fixed type order within each subsystem.
func renderChangelog(rng string, commits []changelogCommit) string {
	// group by subsystem -> type
	subs := map[string]map[string][]changelogCommit{}
	for _, c := range commits {
		types, ok := subs[c.Scope]
		if !ok {
			types = map[string][]changelogCommit{}
			subs[c.Scope] = types
		}
		types[c.Type] = append(types[c.Type], c)
	}
	subNames := make([]string, 0, len(subs))
	for name := range subs {
		subNames = append(subNames, name)
	}
	sort.Strings(subNames)

	var b strings.Builder
	fmt.Fprintf(&b, "Changelog: %s — %d commits\n", rng, len(commits))
	for _, sub := range subNames {
		fmt.Fprintf(&b, "\n## %s\n", sub)
		for _, typ := range changelogTypes {
			group := subs[sub][typ]
			if len(group) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n### %s\n", typ)
			for _, c := range group {
				fmt.Fprintf(&b, "- %s (%s)\n", c.Subject, c.Hash)
			}
		}
	}
	return b.String()
}
