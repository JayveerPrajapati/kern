package execution

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxArtifactBytes caps how large a collected artifact may be (1 MiB).
const maxArtifactBytes = 1 << 20

// artifactExts are the file extensions considered interesting artifacts.
var artifactExts = map[string]bool{
	".diff":  true,
	".patch": true,
	".log":   true,
	".txt":   true,
	".json":  true,
}

// Artifact is a generated file or set of files produced by an execution.
type Artifact struct {
	Name      string // artifact name (e.g. "patch.diff", "test-results.txt")
	Content   string // artifact content
	Path      string // where the artifact lives (relative to project root)
	CreatedAt time.Time
}

// CollectArtifacts scans the given directory for interesting artifacts.
// For now: looks for .diff/.patch/.log/.txt/.json files that are small (<1MB)
// and returns them as Artifact structs.
func CollectArtifacts(dir string) ([]Artifact, error) {
	var arts []Artifact
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		if !artifactExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if info.Size() > maxArtifactBytes {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		arts = append(arts, Artifact{
			Name:      d.Name(),
			Content:   string(data),
			Path:      rel,
			CreatedAt: time.Now(),
		})
		return nil
	})
	return arts, err
}

// WriteArtifact writes an artifact to the project root under .kern/artifacts/.
// It returns the absolute path written.
func WriteArtifact(root string, a Artifact) (string, error) {
	artifactDir := filepath.Join(root, ".kern", "artifacts")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return "", err
	}
	name := a.Name
	if name == "" {
		name = filepath.Base(a.Path)
	}
	if name == "" || name == "." {
		name = "artifact.txt"
	}
	path := filepath.Join(artifactDir, filepath.Base(name))
	if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
