package graphcli

import (
	"github.com/JayveerPrajapati/kern/internal/cliflags"
)

// flags holds the parsed command-line flags the graph/index/search family
// reads — the family's slice of the former cmd/kern flags struct, moved
// with the code. Only the fields these commands consume are kept.
type flags struct {
	all           bool   // --all (ast: include all entries)
	bridgesOnly   bool   // --bridges-only (hubs: emit only the coupling-point report)
	cypher        bool   // --cypher (graph: Cypher output)
	depth         int    // --depth (explore/near: traversal depth; unset default -1)
	entities      bool   // --entities (graph: digital-twin entity nodes)
	etag          string // --etag (explore: conditional-fetch etag from a previous response)
	explain       bool   // --explain (explore: append the why-rationale section)
	force         bool   // --force (index: force overwrite)
	from          string // --from (path: start symbol)
	full          bool   // --full (communities: full unfolded output)
	graphml       bool   // --graphml (graph: GraphML output)
	help          bool   // --help / -h (accepted, never read by the family)
	html          bool   // --html (graph: HTML output)
	interval      int    // --interval (precache/watch: seconds between passes)
	json          bool   // --json (emit JSON output)
	limit         int    // --limit (cap results at N)
	lines         int    // --lines/--line (line count)
	max           int    // --max (maximum count/threshold)
	maxTokens     int    // --max-tokens (token cap)
	path          string // --path (search: scope results to a repo-relative file/dir)
	mermaid       bool   // --mermaid (graph: Mermaid output)
	minConfidence string // --min-confidence (minimum confidence)
	once          bool   // --once (precache: single pass; default)
	oneLine       bool   // --one-line (graph: single-line call-graph neighbourhood)
	out           string // --out (graph/wiki: write output to FILE/DIR)
	range_        string // --range (churn/cochange: line range a..b)
	repos         bool   // --repos (search: scan across repositories)
	root          string // --root (project root, default .)
	semantic      bool   // --semantic (search: semantic matching)
	status        bool   // --status (index: status mode)
	statusFilter  string // string form of --status (the flight-list dual-form quirk, preserved verbatim)
	strict        bool   // --strict (index: strict mode)
	to            string // --to (path: end symbol)
	update        bool   // --update (index: update mode)
}

// parseFlags parses the family's flags via the shared cliflags.Parser,
// mirroring the cmd/kern unified
// parser semantics the moved code depended on (preserved verbatim):
//
//  1. TRAILING VALUE-LESS FLAGS ARE ACCEPT-AND-DEFAULT (a value flag at the
//     end of the line with no value is accepted and left at its default).
//  2. --flag=value AND --flag value FORMS ARE BOTH ACCEPTED for value flags;
//     bool flags accept --flag, --flag=true and --flag=false.
//  3. Unknown flags are rejected with "unknown flag: X" (fail loud, usage
//     error, exit 2 via the caller's fatalUsage). Negative numbers and a
//     bare "-" (stdin) are positionals, not flags.
//  4. Invalid integer values fail loud: "FLAG: invalid integer \"V\"".
//  5. Positionals keep their order in rest; the parser does NOT stop at the
//     first positional.
func parseFlags(args []string) (flags, []string, error) {
	var f flags
	f.depth = -1
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--all":
			p.SetBool(&f.all, "--all", inline, hasInline)
		case "--bridges-only":
			p.SetBool(&f.bridgesOnly, "--bridges-only", inline, hasInline)
		case "--cypher":
			p.SetBool(&f.cypher, "--cypher", inline, hasInline)
		case "--depth":
			p.SetInt(&f.depth, "--depth", inline, hasInline)
		case "--entities":
			p.SetBool(&f.entities, "--entities", inline, hasInline)
		case "--etag":
			p.SetStr(&f.etag, inline, hasInline)
		case "--explain":
			p.SetBool(&f.explain, "--explain", inline, hasInline)
		case "--force":
			p.SetBool(&f.force, "--force", inline, hasInline)
		case "--from":
			p.SetStr(&f.from, inline, hasInline)
		case "--full":
			p.SetBool(&f.full, "--full", inline, hasInline)
		case "--graphml":
			p.SetBool(&f.graphml, "--graphml", inline, hasInline)
		case "--html":
			p.SetBool(&f.html, "--html", inline, hasInline)
		case "--interval":
			p.SetInt(&f.interval, "--interval", inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--limit":
			p.SetInt(&f.limit, "--limit", inline, hasInline)
		case "--lines", "--line":
			// Both spellings report invalid values as --line (the historical
			// quirk, preserved verbatim).
			p.SetInt(&f.lines, "--line", inline, hasInline)
		case "--max":
			p.SetInt(&f.max, "--max", inline, hasInline)
		case "--max-tokens":
			p.SetInt(&f.maxTokens, "--max-tokens", inline, hasInline)
		case "--mermaid":
			p.SetBool(&f.mermaid, "--mermaid", inline, hasInline)
		case "--min-confidence":
			p.SetStr(&f.minConfidence, inline, hasInline)
		case "--once":
			p.SetBool(&f.once, "--once", inline, hasInline)
		case "--one-line":
			p.SetBool(&f.oneLine, "--one-line", inline, hasInline)
		case "--path":
			p.SetStr(&f.path, inline, hasInline)
		case "--out":
			p.SetStr(&f.out, inline, hasInline)
		case "--range":
			p.SetStr(&f.range_, inline, hasInline)
		case "--repos":
			p.SetBool(&f.repos, "--repos", inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--semantic":
			p.SetBool(&f.semantic, "--semantic", inline, hasInline)
		case "--status":
			// Dual-form flag: bool (index: `kern index --status`) AND string
			// (flight list: `kern flight list --status ok|--status=ok`). The
			// next token is never consumed, so `kern index --status <root>`
			// keeps its positional root; the inline bool quirk
			// (`--status=false` sets the bool AND records "false" as the
			// string filter) is preserved verbatim.
			f.status = true
			if hasInline {
				f.statusFilter = inline
			}
		case "--strict":
			p.SetBool(&f.strict, "--strict", inline, hasInline)
		case "--to":
			p.SetStr(&f.to, inline, hasInline)
		case "--update":
			p.SetBool(&f.update, "--update", inline, hasInline)
		case "--help", "-h":
			f.help = true
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}
