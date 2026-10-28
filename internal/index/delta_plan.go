package index

// delta_plan.go holds the pure delta bookkeeping shared by the sqlite and
// nosqlite builds: the plan type Update fills in and the diff helpers it is
// built from. Untagged on purpose — update.go, update_merge.go, engine.go and
// the nosqlite stub all reference these symbols; only the SQLite write side
// (SaveSQLiteDelta in delta_save.go) is gated on !nosqlite.

// deltaPlan names what an Update changed: files and call owners (a changed
// interface recomputes dispatch edges for implementers in untouched files).
type deltaPlan struct {
	changed, removed []string
	owners           []string
}

// diffHashes returns changed/new and removed files between two hash maps.
func diffHashes(prev, cur map[string]string) (changed, removed []string) {
	for file, hash := range cur {
		if old, ok := prev[file]; !ok || old != hash {
			changed = append(changed, file)
		}
	}
	for file := range prev {
		if _, ok := cur[file]; !ok {
			removed = append(removed, file)
		}
	}
	return changed, removed
}

func sameHashes(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for file, hash := range a {
		if b[file] != hash {
			return false
		}
	}
	return true
}

// diffOwners returns owners whose edge list differs (including vanished ones).
func diffOwners(prev, cur map[string][]CallEdge) []string {
	var out []string
	for owner, edges := range cur {
		if !sameEdges(prev[owner], edges) {
			out = append(out, owner)
		}
	}
	for owner := range prev {
		if _, ok := cur[owner]; !ok {
			out = append(out, owner)
		}
	}
	return out
}

func sameEdges(a, b []CallEdge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
