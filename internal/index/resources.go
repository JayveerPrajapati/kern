package index

import "runtime"

// Resource-adaptive tuning.
//
// The indexing pipeline derives its concurrency and memory limits from the
// machine it runs on (CPU count + total physical memory, detected with
// stdlib-only mechanisms, no cgo, no build tags, no new dependencies)
// instead of fixed magic numbers. Every choice is a pure, deterministic
// function of the detected profile, so the same machine always makes the
// same tuning choices — there is no randomness and no time-based variation.
//
// Explicit caller options (WithWorkers, WithMaxFileBytes) always win: when a
// caller sets one, the machine-derived default for that field is ignored and
// existing callers that pass explicit config see zero behavior change.

// Tunable limits: the ceilings/floors that keep adaptive choices sane on
// extreme hardware. They are not per-machine magic numbers.
const (
	// maxWorkers caps the adaptive parse/scan worker pool. Per-file work
	// (ReadFile, hashing, language detection, AST/regex extraction) is
	// single-core-bound and saturates quickly; beyond ~32 workers the
	// ordered-merge and per-worker memory overheads dominate any parse
	// gains. 32 comfortably covers any dev machine while keeping the pool's
	// memory footprint bounded.
	maxWorkers = 32

	// memPerWorker is the memory budget assumed per parse worker (a source
	// file's bytes in flight plus its result structs). The adaptive worker
	// count never exceeds total RAM / memPerWorker, so memory-constrained
	// machines don't spawn a pool that thrashes.
	memPerWorker = 512 << 20 // 512 MiB

	// parallelMinFloor/parallelMinCap bound the adaptive "smallest job count
	// worth a worker pool" threshold (historically a fixed 256 for every
	// machine). It scales with the worker count because a bigger pool has
	// more setup + ordered-merge overhead to amortize.
	parallelMinFloor = 256
	parallelMinCap   = 4096

	// maxFileBytes is the historical fixed limit (10 MiB) and the floor of
	// the memory-scaled limit: machines with little or undetectable RAM keep
	// today's exact behavior. Larger files (e.g. generated .json, bundled
	// .min.js) are skipped to avoid loading huge blobs into RAM and
	// regex-parsing them.
	maxFileBytes = 10 * 1024 * 1024

	// maxFileBytesCap is the ceiling of the memory-scaled limit. Even on
	// huge machines, regex/AST-parsing a >64 MiB file does not amortize, and
	// generated/bundled artifacts that size are never project source.
	maxFileBytesCap = 64 * 1024 * 1024
)

// resourceProfile is the machine snapshot adaptive defaults derive from.
// Zero mem means total physical memory is undetectable on this platform (or
// detection failed), and tuning falls back to CPU-only — never an error.
type resourceProfile struct {
	cpus int
	mem  int64 // total physical memory in bytes; 0 = undetectable
}

// resourceOverrides lets internal/index tests inject fake hardware values so
// adaptive behavior can be asserted without depending on the host. Zero
// values mean "no override" and real detection runs; a negative mem forces
// "undetectable memory" (mem = 0) for fallback tests on memory-rich hosts.
// Package-private: only in-package tests can set it; production callers
// cannot trip it.
var resourceOverrides = struct {
	cpus int
	mem  int64
}{}

// resolveResources detects the host's CPU count and total physical memory.
// Detection never errors: any platform or probe failure degrades to a
// CPU-only profile (mem = 0). Deterministic per machine: same hardware →
// same profile → same tuning.
func resolveResources() resourceProfile {
	cpus := runtime.NumCPU()
	if resourceOverrides.cpus > 0 {
		cpus = resourceOverrides.cpus
	}
	if cpus < 1 {
		cpus = 1
	}
	p := resourceProfile{cpus: cpus}
	switch {
	case resourceOverrides.mem > 0:
		p.mem = resourceOverrides.mem
	case resourceOverrides.mem < 0:
		p.mem = 0 // test hook: force the undetectable-memory fallback
	default:
		p.mem = platformMemTotal()
	}
	return p
}

// tunables holds the resource-adaptive settings resolved for one build.
type tunables struct {
	workers      int   // parse/scan worker pool size
	resultBuf    int   // result channel buffer: one in-flight result per worker
	parallelMin  int   // smallest job count worth the pool; below it, serial
	maxFileBytes int64 // largest file the index will read and scan
}

// resolveTunables derives adaptive defaults from the machine profile and
// overlays explicit caller options. A non-zero explicit option always wins;
// zero fields fall back to the machine-derived value. Pure and
// deterministic: same profile + same options → same tunables.
func resolveTunables(cfg *buildConfig, p resourceProfile) tunables {
	// Concurrency: CPU-derived, capped, then memory-bounded. Each worker
	// holds a source file in RAM while parsing, so on machines where memory
	// is detectable the pool never exceeds total RAM / memPerWorker.
	cpuWorkers := clamp(p.cpus, 1, maxWorkers)
	memWorkers := maxWorkers
	if p.mem > 0 {
		memWorkers = clamp(int(p.mem/int64(memPerWorker)), 1, maxWorkers)
	}
	workers := cpuWorkers
	if memWorkers < workers {
		workers = memWorkers
	}
	if cfg.workers > 0 {
		workers = cfg.workers
	}

	// Buffer sized to the pool: one in-flight result per worker, so a fast
	// worker never blocks behind the merge loop and the main goroutine never
	// waits on a full queue.
	resultBuf := workers

	// A bigger pool has more setup + ordered-merge overhead to amortize, so
	// the serial threshold scales with it: 256 at ≤4 workers, 4096 at 64+.
	parallelMin := clamp(workers*64, parallelMinFloor, parallelMinCap)

	// Memory-scaled file limit: total RAM / 1024 (a file 1/1024th of RAM is
	// a safe read), floored at the historical 10 MiB so small or
	// undetectable-memory machines see zero change, capped at 64 MiB.
	maxBytes := int64(maxFileBytes)
	if p.mem > 0 {
		maxBytes = clamp64(p.mem/1024, maxFileBytes, maxFileBytesCap)
	}
	if cfg.maxBytes > 0 {
		maxBytes = cfg.maxBytes
	}

	return tunables{
		workers:      workers,
		resultBuf:    resultBuf,
		parallelMin:  parallelMin,
		maxFileBytes: maxBytes,
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clamp64(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
