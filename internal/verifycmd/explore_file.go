package verifycmd

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// defaultFileWindow caps a line-range read that names only start_line.
const defaultFileWindow = 200

// exploreOnlyArgs are kern_explore arguments that mean nothing to a file read.
var exploreOnlyArgs = []string{"symbol", "depth", "max", "explain", "max_tokens", "min_confidence", "with_freshness"}

// exploreFilePath reports whether kern_explore's symbol argument names an
// existing regular file, so a path can be explored like a symbol. A qualified
// symbol ("pkg/dir.Func") never matches: it has to exist on disk as written.
func ExploreFilePath(roots []string, args map[string]any) (string, bool) {
	sym := strings.TrimSpace(mcpargs.ArgString(args, "symbol"))
	if sym == "" || strings.ContainsAny(sym, " \t\r\n") {
		return "", false
	}
	if ext := filepath.Ext(sym); ext == "" || len(ext) > 8 {
		return "", false
	}
	bases := append([]string{mcpargs.ArgString(args, "root")}, roots...)
	bases = append(bases, "")
	for _, b := range bases {
		p := sym
		if !filepath.IsAbs(sym) && b != "" {
			p = filepath.Join(b, sym)
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return sym, true
		}
	}
	return "", false
}

// exploreFile serves a file through the kern_compact_file handler, so the
// scope gate, root confinement and token accounting are the ones compact
// already has. start_line/end_line read a verbatim line window.
func ExploreFile(ctx context.Context, compact func(context.Context, map[string]any) (string, error), path string, args map[string]any) (string, error) {
	cargs := make(map[string]any, len(args))
	for k, v := range args {
		cargs[k] = v
	}
	for _, k := range exploreOnlyArgs {
		delete(cargs, k)
	}
	cargs["path"] = path

	start, end := lineArg(args["start_line"]), lineArg(args["end_line"])
	if start == 0 && end == 0 {
		return compact(ctx, cargs)
	}
	cargs["tier"] = "full"
	delete(cargs, "etag")
	body, err := compact(ctx, cargs)
	if err != nil {
		return "", err
	}
	return sliceLines(path, body, start, end)
}

func lineArg(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

func sliceLines(path, body string, start, end int) (string, error) {
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start + defaultFileWindow - 1
	}
	if start > len(lines) {
		return "", fmt.Errorf("start_line %d is past the end of %s (%d lines)", start, path, len(lines))
	}
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s lines %d-%d of %d:\n", path, start, end, len(lines))
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i, lines[i-1])
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
