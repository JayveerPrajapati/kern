package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeResources sets the test override hook for the duration of a test and
// restores it afterwards, so tests never leak fake hardware values into the
// real detection path. Zero cpus/mem means "no override" (real detection);
// a negative mem forces the undetectable-memory fallback.
func fakeResources(t *testing.T, cpus int, mem int64) {
	t.Helper()
	prev := resourceOverrides
	resourceOverrides.cpus = cpus
	resourceOverrides.mem = mem
	t.Cleanup(func() { resourceOverrides = prev })
}

func TestResolveResourcesOverrideHook(t *testing.T) {
	fakeResources(t, 16, 16<<30) // 16 CPUs, 16 GiB
	p := resolveResources()
	if p.cpus != 16 {
		t.Fatalf("override cpus: got %d, want 16", p.cpus)
	}
	if p.mem != 16<<30 {
		t.Fatalf("override mem: got %d, want %d", p.mem, int64(16)<<30)
	}
	// Zero override → real host detection; NumCPU is always >= 1.
	fakeResources(t, 0, 0)
	p = resolveResources()
	if p.cpus < 1 {
		t.Fatalf("real detection cpus: got %d, want >= 1", p.cpus)
	}
	// Negative mem sentinel → forced undetectable, even on memory-rich hosts.
	fakeResources(t, 8, -1)
	p = resolveResources()
	if p.mem != 0 {
		t.Fatalf("forced-undetectable mem: got %d, want 0", p.mem)
	}
	if p.cpus != 8 {
		t.Fatalf("forced-undetectable cpus: got %d, want 8", p.cpus)
	}
}

func TestResolveTunablesCPUOnly(t *testing.T) {
	cases := []struct {
		name     string
		cpus     int
		wantWork int
		wantBuf  int
		wantMin  int
		wantMaxB int64
	}{
		{"single core", 1, 1, 1, 256, maxFileBytes},
		{"quad core", 4, 4, 4, 256, maxFileBytes},
		{"eight cores", 8, 8, 8, 512, maxFileBytes},
		{"16 cores", 16, 16, 16, 1024, maxFileBytes},
		{"many cores capped", 128, 32, 32, 2048, maxFileBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// mem = 0: undetectable memory → pure CPU-only tuning.
			tuns := resolveTunables(&buildConfig{}, resourceProfile{cpus: tc.cpus, mem: 0})
			if tuns.workers != tc.wantWork {
				t.Errorf("workers: got %d, want %d", tuns.workers, tc.wantWork)
			}
			if tuns.resultBuf != tc.wantBuf {
				t.Errorf("resultBuf: got %d, want %d", tuns.resultBuf, tc.wantBuf)
			}
			if tuns.parallelMin != tc.wantMin {
				t.Errorf("parallelMin: got %d, want %d", tuns.parallelMin, tc.wantMin)
			}
			// Undetectable memory must keep the historical 10 MiB floor.
			if tuns.maxFileBytes != tc.wantMaxB {
				t.Errorf("maxFileBytes: got %d, want %d", tuns.maxFileBytes, tc.wantMaxB)
			}
		})
	}
}

func TestResolveTunablesMemoryAware(t *testing.T) {
	cases := []struct {
		name     string
		cpus     int
		mem      int64
		wantWork int
	}{
		// 64 cores but only 4 GiB RAM: the memory budget wins
		// (4 GiB / 512 MiB = 8 workers, not 32).
		{"memory bound wins", 64, 4 << 30, 8},
		// 8 cores, 16 GiB: CPU bound (8 < 16 GiB / 512 MiB = 32).
		{"cpu bound wins", 8, 16 << 30, 8},
		// 128 cores, 256 GiB: both sides hit their caps → 32.
		{"both capped", 128, 256 << 30, 32},
		// 64 cores, 8 GiB: 8 GiB / 512 MiB = 16 workers.
		{"mid memory", 64, 8 << 30, 16},
		// Tiny machine: floor at 1 worker.
		{"tiny memory floor", 32, 128 << 20, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tuns := resolveTunables(&buildConfig{}, resourceProfile{cpus: tc.cpus, mem: tc.mem})
			if tuns.workers != tc.wantWork {
				t.Errorf("workers: got %d, want %d", tuns.workers, tc.wantWork)
			}
			if tuns.resultBuf != tuns.workers {
				t.Errorf("resultBuf: got %d, want %d", tuns.resultBuf, tuns.workers)
			}
		})
	}
}

func TestResolveTunablesMaxFileBytesScales(t *testing.T) {
	cases := []struct {
		name  string
		mem   int64
		wantB int64
	}{
		// Undetectable memory: historical floor, no change.
		{"undetectable", 0, maxFileBytes},
		// 8 GiB: 8 MiB < floor → clamped to the historical 10 MiB.
		{"small machine floored", 8 << 30, maxFileBytes},
		// 16 GiB: RAM / 1024 = 16 MiB.
		{"16 GiB", 16 << 30, 16 << 20},
		// 32 GiB → 32 MiB.
		{"32 GiB", 32 << 30, 32 << 20},
		// 512 GiB: 512 MiB > cap → clamped to 64 MiB.
		{"huge machine capped", 512 << 30, maxFileBytesCap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tuns := resolveTunables(&buildConfig{}, resourceProfile{cpus: 8, mem: tc.mem})
			if tuns.maxFileBytes != tc.wantB {
				t.Errorf("maxFileBytes: got %d, want %d", tuns.maxFileBytes, tc.wantB)
			}
		})
	}
}

func TestResolveTunablesExplicitOptionsWin(t *testing.T) {
	// A profile that would otherwise choose 32 workers / 64 MiB files.
	cfg := &buildConfig{workers: 2, maxBytes: 12345}
	tuns := resolveTunables(cfg, resourceProfile{cpus: 128, mem: 256 << 30})
	if tuns.workers != 2 {
		t.Errorf("explicit workers: got %d, want 2", tuns.workers)
	}
	if tuns.maxFileBytes != 12345 {
		t.Errorf("explicit maxFileBytes: got %d, want 12345", tuns.maxFileBytes)
	}
	// Derived values follow the explicit worker count, not the machine.
	if tuns.resultBuf != 2 {
		t.Errorf("resultBuf from explicit workers: got %d, want 2", tuns.resultBuf)
	}
	if tuns.parallelMin != parallelMinFloor {
		t.Errorf("parallelMin from explicit workers: got %d, want %d", tuns.parallelMin, parallelMinFloor)
	}
	// Zero options → machine-derived values apply.
	tuns = resolveTunables(&buildConfig{}, resourceProfile{cpus: 128, mem: 256 << 30})
	if tuns.workers != maxWorkers {
		t.Errorf("default workers: got %d, want %d", tuns.workers, maxWorkers)
	}
	if tuns.maxFileBytes != maxFileBytesCap {
		t.Errorf("default maxFileBytes: got %d, want %d", tuns.maxFileBytes, maxFileBytesCap)
	}
}

// adaptiveFixture writes a Go file that is comfortably larger than the 10 MiB
// floor but well under the 64 MiB cap, with no long lines (isMinified would
// reject a single >5000-char line) so it is indexable whenever the size limit
// admits it.
func adaptiveFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	line := "// " + strings.Repeat("x", 400) + "\n"
	var buf bytes.Buffer
	buf.WriteString("package big\n")
	for i := 0; i < 27000; i++ {
		buf.WriteString(line)
	}
	big := filepath.Join(dir, "big.go")
	if err := os.WriteFile(big, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if int64(buf.Len()) <= maxFileBytes {
		t.Fatalf("fixture too small: %d bytes, need > %d", buf.Len(), maxFileBytes)
	}
	if int64(buf.Len()) >= maxFileBytesCap {
		t.Fatalf("fixture too large: %d bytes, need < %d", buf.Len(), maxFileBytesCap)
	}
	return dir
}

func TestBuildAdaptiveMaxFileBytesEndToEnd(t *testing.T) {
	dir := adaptiveFixture(t)
	// Undetectable memory → 10 MiB floor → the 10.4 MiB file is skipped.
	fakeResources(t, 8, -1)
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.FileHashes["big.go"]; ok {
		t.Fatal("undetectable-memory build must skip a file above the 10 MiB floor")
	}
	// 32 GiB RAM → 32 MiB limit → the same file is now indexed.
	fakeResources(t, 8, 32<<30)
	ix, err = Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.FileHashes["big.go"]; !ok {
		t.Fatal("32 GiB build must index a file under the 32 MiB limit")
	}
	// Explicit option always wins: WithMaxFileBytes(1024) skips it even on
	// the memory-rich profile.
	fakeResources(t, 8, 32<<30)
	ix, err = BuildWithOptions(dir, WithMaxFileBytes(1024))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.FileHashes["big.go"]; ok {
		t.Fatal("explicit WithMaxFileBytes(1024) must skip a 10 MiB file")
	}
}

func TestBuildExplicitWorkersSanity(t *testing.T) {
	dir := adaptiveFixture(t)
	fakeResources(t, 64, 256<<30) // would resolve 32 workers by default
	one, err := BuildWithOptions(dir, WithWorkers(1))
	if err != nil {
		t.Fatal(err)
	}
	dflt, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Worker count must not change index content: same files, same hashes.
	if len(one.FileHashes) != len(dflt.FileHashes) {
		t.Fatalf("file count mismatch: explicit=%d default=%d", len(one.FileHashes), len(dflt.FileHashes))
	}
	for rel, h := range one.FileHashes {
		if dflt.FileHashes[rel] != h {
			t.Fatalf("hash mismatch for %s between explicit and default build", rel)
		}
	}
}
