package secscan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestScanParallelBudget pins the serial-scan regression: the scan was serial and took ~8 s
// for ~2k files. This gate writes 2k small Go/config files and requires the
// whole scan (walk + read + rules) to finish in 4 s — comfortably above the
// measured ~1.5 s on 8 cores, far below the serial regression.
func TestScanParallelBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("performance gate: skipped in -short")
	}
	root := t.TempDir()
	const files = 2000
	const perDir = 50
	body := []byte(`package fixture

import (
	"database/sql"
	"fmt"
)

func Handler(db *sql.DB, name string) {
	db.Query(fmt.Sprintf("select * from t where n = %s", name))
}
`)
	for i := 0; i < files; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%02d", i/perDir))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.go", i)), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	findings, err := Scan(root)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("Scan found nothing in a fixture full of dynamic SQL")
	}
	if elapsed > 4*time.Second {
		t.Fatalf("Scan of %d files took %s (budget 4s) — the serial scan is back", files, elapsed)
	}
}
