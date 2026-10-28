package destrules

// stateful.go classifies commands that destroy state kern cannot see: remote
// git history, cluster objects, cloud storage, database rows, container
// volumes — plus a whole-tree discard of uncommitted work. They are "destructive"
// in the sense the guard cares about: the loss is irreversible or reaches
// outside the repository, so the operator must be asked.
//
// The bash oracle already consults this package for its destructive verdict
// (guardrule.gateSegmentHit), so adding a rule here closes the gap in BOTH
// surfaces at once and keeps `kern guard parity` green by construction.
//
// Deliberately narrow: routine forms of the same tools stay governed, not
// gated (`git push origin main`, `kubectl get pods`, `docker ps`, `psql -c
// 'select 1'`, `git checkout -- file.go`, `git restore --staged .`). A guard
// that gates the everyday command gets switched off, and then it protects
// nothing.

import "strings"

// forcePushFlags are the spellings that rewrite published history.
var forcePushFlags = map[string]bool{
	"-f":                  true,
	"--force":             true,
	"--force-with-lease":  true,
	"--force-if-includes": true,
	"--mirror":            true, // deletes remote refs the local repo lacks
}

// stateDestroyingKinds are kubectl targets whose deletion is not recoverable
// from the repo or the cluster itself.
var stateDestroyingKinds = map[string]bool{
	"namespace": true, "namespaces": true, "ns": true,
	"pv": true, "persistentvolume": true, "persistentvolumes": true,
	"pvc": true, "persistentvolumeclaim": true, "persistentvolumeclaims": true,
	"secret": true, "secrets": true,
}

// classifyStateful matches the outside-the-repo destruction family.
func ClassifyStateful(name, seg string, tokens []string) (bool, string) {
	switch name {
	case "git":
		return classifyGit(name, tokens)
	case "kubectl", "oc":
		return classifyKubectl(tokens)
	case "terraform", "tofu":
		for _, t := range tokens[1:] {
			if t == "destroy" {
				return true, name + " destroy tears down managed infrastructure"
			}
		}
	case "aws":
		return classifyAWSS3(tokens)
	case "psql", "mysql", "mariadb", "sqlite3", "clickhouse-client":
		upper := strings.ToUpper(seg)
		for _, kw := range []string{"DROP TABLE", "DROP DATABASE", "DROP SCHEMA", "TRUNCATE TABLE", "DELETE FROM"} {
			if strings.Contains(upper, kw) {
				return true, name + " executes " + strings.ToLower(kw)
			}
		}
	case "docker", "podman":
		return classifyContainers(name, tokens)
	}
	return false, ""
}

// classifyGit covers force pushes and a whole-tree discard of local work.
func classifyGit(name string, tokens []string) (bool, string) {
	if len(tokens) < 2 {
		return false, ""
	}
	switch tokens[1] {
	case "push":
		// Any push publishes to a remote. Operator policy gates it at EVERY
		// surface (bash guard and exec firewall) so neither is a bypass; a
		// dry run writes nothing and stays benign.
		for _, t := range tokens[2:] {
			if t == "--dry-run" || t == "-n" {
				return false, ""
			}
		}
		for _, t := range tokens[2:] {
			if forcePushFlags[t] {
				return true, "git push " + t + " rewrites remote history"
			}
			// `git push origin +main` — the refspec + spelling of --force.
			if strings.HasPrefix(t, "+") && !strings.HasPrefix(t, "++") {
				return true, "git push " + t + " force-updates the remote ref"
			}
		}
		return true, "git push publishes to a remote (approval required)"
	case "checkout", "restore":
		// `git checkout -- .` / `git checkout .` / `git restore .` discard
		// EVERY uncommitted change in the tree. A single path is routine.
		stagedOnly := false
		for _, t := range tokens[2:] {
			if t == "--staged" || strings.HasPrefix(t, "--staged=") {
				stagedOnly = true
			}
		}
		if stagedOnly {
			return false, ""
		}
		for _, t := range tokens[2:] {
			if t == "--" {
				continue
			}
			if t == "." || t == "./" {
				return true, "git " + tokens[1] + " " + t + " discards all uncommitted changes"
			}
		}
	}
	return false, ""
}

// classifyKubectl gates deletion of cluster state that the repo cannot restore.
func classifyKubectl(tokens []string) (bool, string) {
	if len(tokens) < 3 || tokens[1] != "delete" {
		return false, ""
	}
	for _, t := range tokens[2:] {
		switch {
		case stateDestroyingKinds[strings.ToLower(t)]:
			return true, "kubectl delete " + t
		case t == "--all" || t == "-A" || t == "--all-namespaces":
			return true, "kubectl delete " + t + " affects every matching object"
		case t == "-f" || t == "--filename" || strings.HasPrefix(t, "--filename="):
			return true, "kubectl delete -f deletes every object in the manifest"
		}
	}
	return false, ""
}

// classifyAWSS3 gates recursive S3 deletion.
func classifyAWSS3(tokens []string) (bool, string) {
	for i, t := range tokens {
		if t != "s3" || i+1 >= len(tokens) {
			continue
		}
		sub := tokens[i+1]
		if sub != "rm" && sub != "rb" {
			continue
		}
		for _, a := range tokens[i+2:] {
			if a == "--recursive" || a == "--force" {
				return true, "aws s3 " + sub + " " + a + " deletes remote objects"
			}
		}
	}
	return false, ""
}

// classifyContainers gates container-volume/image pruning, which deletes data
// that no manifest can restore.
func classifyContainers(name string, tokens []string) (bool, string) {
	args := tokens[1:]
	has := func(want string) bool {
		for _, t := range args {
			if t == want {
				return true
			}
		}
		return false
	}
	if has("prune") && (has("system") || has("volume") || has("volume") || has("image") || has("container") || has("builder")) {
		return true, name + " prune deletes images/volumes/containers"
	}
	if has("volume") && (has("rm") || has("remove")) {
		return true, name + " volume rm deletes persisted data"
	}
	if has("compose") && has("down") && has("-v") {
		return true, name + " compose down -v deletes named volumes"
	}
	return false, ""
}
