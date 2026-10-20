# Symbol identity and traversal

`index.Symbol.DeclarationID()` identifies a declaration within a repository
snapshot. It hashes the normalized repository-relative file path, language,
declaration kind, enclosing declaration, receiver, name, declaration line, and
parameter list. Confidence, entry-point flags, and body end lines do not change
the identity. IDs are deterministic for the same declaration metadata, but are
not persistent rename/move tracking IDs: changing a path, declaration line, or
signature changes the ID.

Blast-radius traversal uses declaration IDs internally, not display names.
Same-named wrappers and callers remain separate nodes, including in cycles and
multi-root queries. Unique display names remain unchanged; collisions are
qualified by package, with an identity suffix if package qualification is still
ambiguous. Depths and source symbols remain aligned with the displayed radius.

Source-owned call edges supply caller candidates even when legacy pooled name
buckets cannot attribute them. Shared language/import binding validates the
callee. A source file containing multiple declarations with the same owner name
does not provide enough information to select an overload: that caller remains
an unresolved, unexpanded leaf rather than selecting a declaration arbitrarily.
Non-synthetic legacy edges without source provenance retain the existing
resolver behavior; these are compatibility guesses, not proven bindings.

SQLite persists the enclosing declaration as well as call source files, so
declaration identity survives save/load. Cache version 21 forces older indexes
to rebuild. `nosqlite` builds retain JSON identity-label persistence and return
the standard unavailable error for direct SQLite metadata operations.

## Checks

```sh
go test ./internal/index ./internal/intel -run 'TestDeclarationID|TestSQLiteDeclarationIdentity|TestBlastRadius'
go test -tags nosqlite ./internal/index -run TestPersistIdentityLabelWithoutSQLite
go test ./internal/intel -run '^$' -bench BenchmarkBlastRadiusDistinctDeclarations -benchmem
```

The traversal fixtures exercise Go, Python, and TypeScript extraction and
same-named transitive callers. Separate source-ownership fixtures also cover
Java. These are correctness regressions and small-fixture latency benchmarks,
not evidence of complete semantic accuracy across all supported languages.

## Structured graph exports (schema 2)

JSON, GraphML, Cypher, HTML, graph context, and graph packs now separate
declaration identity from readable names. Declared node IDs are
`DeclarationID()` values; names remain labels. `root` retains the query (empty
for whole-repository graphs), while `root_ids` identifies every matching root.
A declaration-ID query selects exactly one declaration. Bare queries retain
all matching declarations instead of silently choosing the first.

Calls are attributed using exact owner full names and `source_file`. Targets
are constrained by language, file/receiver scope, Go package scope, and the
source file's imports. Import paths select the most specific indexed directory,
not an arbitrary same-basename package. Missing provenance resolves an owner
only when it has a unique declaration. Overloaded owners or targets remain
explicit `ref:` nodes with sorted candidate declaration IDs and no fabricated
definition location. Unknown calls remain references with no candidates.

`confidence` / `confidence_label` describe extraction, not semantic binding:
an explicitly parsed call can be EXTRACTED but unresolved. The separate
`resolution` field is `resolved`, `ambiguous`, or `unresolved`. Edges retain
the original `reference`, `source_file`, and synthesized-edge provenance.
Cypher matches IDs rather than names; GraphML and HTML preserve readable labels.
Graph context selects a declaration using its existing entry-resolution policy
and flags unresolved adjacency bindings.

Whole graphs preserve all distinct declaration candidates before applying the
node limit. Ranking counts only the same resolved calls eligible for emission;
ambiguous and external calls do not inflate degree. Only resolved edges are
emitted in whole mode; `unresolved_calls` counts calls without a unique endpoint
binding before the node cap. Neighborhoods include reference nodes. Node caps
keep roots first, filter edges to retained endpoints, and remove discarded
root IDs. Snapshot schema 2 rejects older name-identity snapshots.

## Shared bindings and unified graph

The immutable index caches declaration bindings once per snapshot. Exports,
source-owned caller lookup, traversal ownership, and `intel.FromIndex` reuse
those bindings. Updates construct a fresh index and cache instead of mutating
the previous snapshot. JSON and SQLite retain named imports, namespace aliases,
and dynamic-call markers; SQLite point lookups retain those call markers too.
Import-directory lookup is populated completely before the binding catalog is
published. Calls and inheritance queries only read that lookup; inheritance-only
imports and unknown module paths are included during initialization too.

The unified graph uses schema-2 declaration IDs for symbol nodes and
contains/defines edges. `domain.Symbol.ID` carries identity through context
packets. Labels and uniquely resolvable legacy names remain query inputs, but
`NodeByID` requires an actual ID. Ambiguous labels never select an arbitrary
declaration. Bound call edges carry references, resolution, confidence, and
synthetic provenance. Unknown/ambiguous endpoints are reference nodes that
cannot expand transitive traversals. Plain source-empty legacy calls still
retain raw-name compatibility endpoints.

Inheritance edges bind to type-declaration IDs (`Index.InheritBindings`). The
subtype is resolved by name among type declarations; because `Inherits` records
no source file, a subtype name shared by several types yields an ambiguous
reference with every candidate rather than a guess. Bases resolve through the
subtype's file/package scope, then named imports or qualified import aliases;
an unknown base stays an unresolved reference. Whole-graph and graph-context
community membership uses `Index.DeclarationCommunities`, which clusters
declaration IDs over resolved call bindings only, so same-named declarations
never merge merely because their names match. Community reports, architecture,
wiki frontmatter, and surprising connections also use declaration memberships.
Architecture coupling and surprising-edge rarity count distinct resolved directed
declaration pairs, not repeated call sites or unresolved name guesses. Connector
suppression in surprising connections counts caller packages per declaration.
`Index.DeclarationNames` preserves unique readable names and qualifies collisions
by package, then file/line, then full identity for same-line overloads. These
names are display labels, not identity keys. Ambiguous bare-name community queries
do not select an arbitrary declaration.

The persisted `ix.Communities` map remains a compatibility display aggregate.
Standalone hubs and bridges now count distinct resolved declaration pairs in
both directions. Hub normalization uses declaration-community sizes; hub flags
in wiki and change-risk reports are keyed by declaration ID. Hub and bridge JSON
rows expose `id` alongside readable names and exact definition locations.
Test and fixture declarations are excluded from production dependency degrees.
Caller-only legacy indexes retain an incoming-only compatibility fallback for
unique names; ambiguous names are not attributed, and these legacy records are
not compiler evidence.

### Optional Go compiler bindings

`kern index --go-types [root]` forces a full build with an additional local
`go/packages` / `go/types` pass (`BuildWithOptions(root, WithGoTypes())` in Go).
It reuses the existing pinned `golang.org/x/tools` dependency. This slower pass
is opt-in so ordinary indexing latency is unchanged. Package loading is
read-only and disables module-proxy, checksum-server, and direct VCS downloads.
Dependencies and a compatible Go toolchain must already be available locally.

For type-checked production packages, function calls record exact `from_id`,
`to_id`, and `binding="go/types"`. Promoted methods, aliases, generic function
instantiations, and interface method declarations use the compiler-selected
object rather than a name guess. Separate `init` declarations keep separate
caller identities even in one file. Function-valued variables and returned
callbacks remain unresolved dynamic targets; interface targets identify the
static method, not the implementation selected at runtime.

JSON and SQLite preserve this metadata and the opt-in mode. Incremental updates
rerun the compiler pass, including unchanged callers whose target IDs changed.
Full rebuilds should retain `--go-types` when compiler bindings are wanted.
Ill-typed/unloadable packages, test variants, and files excluded by the active Go
build configuration retain conservative AST/import binding without a compiler
claim. Compiler coverage is therefore per call, not a repository-wide guarantee.
Missing persisted compiler IDs stay unresolved rather than falling back to names.

The Go pass also builds SSA and a class-hierarchy call graph. Interface and
function-value calls gain local, type-compatible possible targets, including
implicit interface implementations; matching a method name alone is insufficient.
These edges carry `binding="go/ssa-cha"`, `synth="dispatch:go-cha"`, and LOW
confidence. They are an overapproximation, not observed runtime executions.
Original static interface references and unresolved callback references remain
in the graph. Anonymous, reflective, unsafe, and external implementations without
indexed declarations are not fabricated. Incremental rebuilds remove old
candidate edges before deriving the new candidate set.

### Optional Java compiler bindings

`kern index --java-compiler [root]` runs a local JDK 17+ javac analysis pass.
`--java-classpath PATH` supplies already-available dependencies; paths use the
platform's classpath separator. Go callers can use `WithJavaCompiler(classpath)`.
Go and Java compiler flags may be combined. These flags require full builds,
not read-only status/where operations; subsequent incremental updates preserve
the selected compiler modes and Java classpath.

The javac API selects overloads using Java's actual typing and conversion rules,
not arity/name guesses. Caller and callee declarations match source location,
enclosing type, and parameter source, distinguishing overloaded callers and
same-line declarations. Explicit constructors are supported when indexed;
compiler-generated default constructors remain references. Unindexed/external
overloads retain distinct compiler signatures, without fabricated locations.

Local virtual overrides gain LOW-confidence `synth="dispatch:javac"` candidates.
javac subtype/override checks exclude unrelated same-named methods and other
overloads. Calls on freshly constructed receivers and explicit `super` calls do
not gain polymorphic override edges. Binding metadata is available in JSON,
GraphML, and Cypher exports and survives JSON/SQLite persistence.

The helper only parses/analyzes source: annotation processors are disabled,
user classes are not run, no class files are generated, and ambient JVM agent
options are excluded. No dependencies are downloaded. Invalid sources or missing
classpath dependencies fail the requested compiler build instead of publishing
partial compiler claims. Default indexing still uses AST/import binding.

Go binding honors explicit, dot, blank, and declared-package-name imports.
Receiver type hints retain their import qualifier. Sequential lexical scopes
track block/closure shadowing rather than flattening all local types into the
function environment. An unknown local shadow of an import namespace is marked
dynamic and remains unresolved. Inferred interface-dispatch edges retain the
original source file and `synth=dispatch`, remain LOW-confidence, and do not
merge unrelated same-named caller declarations. This is inferred reachability,
not proof of the implementation selected at runtime.

Python module/named imports and JavaScript/TypeScript relative namespace,
named, and default imports carry alias and imported-symbol metadata. Supported
adapters are module-scope forms, including multi-line parenthesized/braced
lists. TypeScript type-only imports do not bind runtime
calls. JavaScript and TypeScript can bind across their language family; unrelated
languages cannot bind merely because their symbol names match.

JS/TS cross-module calls and inheritance require explicit export evidence.
Named runtime declarations, local lists such as `export { helper as publicName }`,
and imported-then-exported bindings participate in the same export resolver as
defaults and barrel reexports. Merely finding a declaration in the target file
does not make it exported. Private declarations remain accessible to same-file
calls, but not to named or namespace imports. Explicit exports take precedence
over star reexports; star reexports never forward a default export. Same-directory
JS/TS files do not implicitly share their declarations.

### Accuracy ceiling

This is conservative AST/import binding, not a compiler/type-checker for every
language. Optional Go and Java passes supply compiler-selected declaration IDs,
and Java overload selection follows javac. Other languages still require their
own semantic/compiler adapters; full lexical binding IDs are not provided.
Dispatch candidates are static possibilities, not proof of the actual target
at runtime. Reflection, monkey-patching, dynamic loading, and externally supplied
implementations require runtime evidence and may remain unresolved. Non-Go local
shadowing/type inference is not complete. The import adapter handles
multi-line Python parenthesized and JS/TS braced imports and the named part of
`Default, { named }` clauses. JS/TS default imports, `export default`,
`export { a as b } from`, `export * from`, and barrel chains bind through
`Index.ExportsByFile` (persisted in JSON and the `file_exports` SQLite table).
Python wildcard imports, JS/TS `export * as ns`, CommonJS `require`/`module.exports`,
and configured module-path aliases (tsconfig `paths`) are still not supported.
The export adapter is conservative source parsing, not a complete module grammar:
anonymous/expression defaults, arbitrary destructured or multi-variable exports,
and full type-space export semantics still require a language-aware resolver.
Unrecognized imports and cross-file
bare calls without supported import evidence stay unresolved rather than guessing.
Declaration metadata provides a language-independent identity contract, but
language-specific extraction quality still needs separate conformance tests.

Further accuracy work requires declaration-owned edges at extraction time,
language-aware lexical/import/type binding, and larger per-language
precision/recall corpora. Synthetic multi-language export fixtures test identity
and conservative scoping; they do not measure parser coverage or semantic recall.
