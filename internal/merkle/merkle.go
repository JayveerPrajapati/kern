// Package merkle provides an incremental Merkle tree over string leaves,
// used by the governance audit log for parallel, lock-free tamper-evident
// logging. The tree is deterministic: same leaves in the same order produce
// the same root. It is a pure data structure — no project imports, no
// persistence — extracted from internal/governance (2026-10-21) so the
// audit subtree stays under its ARCHITECTURE.md LOC cap.
package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Tree is an incremental Merkle Tree.
type Tree struct {
	mu     sync.RWMutex
	leaves []string
	levels [][]string // levels[0] is leaves; levels[d] stores pairwise completed nodes
	root   string
}

// NewTree initializes an empty incremental Merkle tree.
func NewTree() *Tree {
	return &Tree{
		levels: make([][]string, 0),
	}
}

// hashNode computes the parent SHA-256 hash from left and right child hashes.
func hashNode(left, right string) string {
	h := sha256.New()
	h.Write([]byte(left + ":" + right))
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeRoot computes the root hash from a slice of leaf hashes.
func ComputeRoot(leaves []string) string {
	if len(leaves) == 0 {
		return ""
	}
	current := make([]string, len(leaves))
	copy(current, leaves)
	for len(current) > 1 {
		var next []string
		for i := 0; i < len(current); i += 2 {
			if i+1 < len(current) {
				next = append(next, hashNode(current[i], current[i+1]))
			} else {
				next = append(next, hashNode(current[i], current[i]))
			}
		}
		current = next
	}
	return current[0]
}

// Append inserts a leaf hash into the incremental Merkle tree in O(log N) time
// and updates the cached tree root.
func (m *Tree) Append(leaf string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.leaves = append(m.leaves, leaf)
	if len(m.levels) == 0 {
		m.levels = append(m.levels, nil)
	}

	level := 0
	m.levels[level] = append(m.levels[level], leaf)

	// Propagate completed pairs up the levels (amortized O(1))
	for len(m.levels[level])%2 == 0 {
		idx := len(m.levels[level])
		parent := hashNode(m.levels[level][idx-2], m.levels[level][idx-1])
		level++
		if level >= len(m.levels) {
			m.levels = append(m.levels, nil)
		}
		m.levels[level] = append(m.levels[level], parent)
	}

	// Roll up unpaired branch nodes to compute the root in O(log N)
	m.root = m.computeRootFromLevelsLocked()
	return m.root
}

// computeRootFromLevelsLocked folds any trailing odd (unpaired) nodes across tree levels.
func (m *Tree) computeRootFromLevelsLocked() string {
	n := len(m.leaves)
	if n == 0 {
		return ""
	}
	if n == 1 {
		return m.leaves[0]
	}

	var folded string
	hasFolded := false
	top := len(m.levels) - 1

	for d := 0; d < top; d++ {
		lvl := m.levels[d]
		if len(lvl)%2 == 1 {
			unpaired := lvl[len(lvl)-1]
			if !hasFolded {
				folded = hashNode(unpaired, unpaired)
				hasFolded = true
			} else {
				folded = hashNode(unpaired, folded)
			}
		} else if hasFolded {
			folded = hashNode(folded, folded)
		}
	}

	topNode := m.levels[top][len(m.levels[top])-1]
	if !hasFolded {
		return topNode
	}
	return hashNode(topNode, folded)
}

// Root returns the current Merkle root hash in O(1) time.
func (m *Tree) Root() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root
}
