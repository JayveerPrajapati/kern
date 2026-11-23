package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdapterFanOutRatchet(t *testing.T) {
	limits := map[string]int{
		"cmd/kern":     83,
		"internal/mcp": 112,
	}
	root := filepath.Join("..", "..")
	for dir, limit := range limits {
		imports := map[string]bool{}
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, `"github.com/JayveerPrajapati/kern/internal/`) {
					imports[strings.Trim(line, `"`)] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
		if len(imports) > limit {
			t.Fatalf("%s imports %d internal packages, ratchet limit is %d", dir, len(imports), limit)
		}
		t.Logf("%s internal dependency fan-out: %d/%d", dir, len(imports), limit)
	}
}
