package index

import (
	"strconv"

	"github.com/JayveerPrajapati/kern/internal/graphcache"
)

// catalogSidecarName is the graphcache sidecar name for the export catalog.
const catalogSidecarName = "graph-catalog"

// sidecarCatalog is the serialized form of the export catalog. exportCall's
// fields are unexported, so records cross the gob boundary through this
// exported mirror; every other table is already exported types.
type sidecarCatalog struct {
	ByID       map[string]Symbol
	ByFullName map[string][]Symbol
	ImportDirs map[string]string
	Records    []sidecarCall
	ByEndpoint map[string][]int
	Files      []string
	Names      map[string]string
}

type sidecarCall struct {
	From GraphNode
	To   GraphNode
	Edge GraphEdge
}

// GraphCacheKey derives the identity key shared by the graphcache sidecars
// (the export catalog here, the built domain graph in internal/app). It
// covers the index identity, the graph schema version, the sidecar format
// generation, and the symbol count — a rebuilt or re-rooted index, or a
// format bump, misses the sidecar and falls back to a fresh build. The key
// deliberately does NOT include the root: storage is already per-root
// (under <root>/.kern/), the identity is content-scoped, and keying on the
// root STRING would make the same repo miss per root spelling (".", an
// absolute path, a test's filepath.Dir chain) — thrashing the sidecar
// between processes. ok=false (no recorded identity, e.g. an in-memory test
// index) disables sidecar persistence entirely for this index.
func (ix *Index) GraphCacheKey() (string, bool) {
	if ix == nil || ix.Identity == nil {
		return "", false
	}
	return graphcache.Key(
		strconv.Itoa(GraphSchemaVersion),
		strconv.Itoa(graphcache.SidecarVersion),
		ix.Identity.ContentRoot,
		ix.Identity.TreeOID,
		strconv.Itoa(len(ix.Symbols)),
	), true
}

// loadCatalogSidecar returns the persisted export catalog for ix, or nil on
// any miss (no key, no file, decode failure). A hit is fully populated:
// records are converted back from the sidecar mirror and every table the
// query surface reads is served from the sidecar, so the hit skips
// buildExportCatalog and buildCalls entirely.
func loadCatalogSidecar(ix *Index) *exportCatalog {
	key, ok := ix.GraphCacheKey()
	if !ok {
		return nil
	}
	var sc sidecarCatalog
	if !graphcache.Load(ix.Root, catalogSidecarName, key, &sc) {
		return nil
	}
	catalog := &exportCatalog{
		index:      ix,
		byID:       sc.ByID,
		byFullName: sc.ByFullName,
		importDirs: sc.ImportDirs,
		byEndpoint: sc.ByEndpoint,
		files:      sc.Files,
		names:      sc.Names,
		records:    make([]exportCall, len(sc.Records)),
	}
	for i, record := range sc.Records {
		catalog.records[i] = exportCall{from: record.From, to: record.To, edge: record.Edge}
	}
	return catalog
}

// saveCatalogSidecar persists the built export catalog, best-effort: a
// read-only sandbox or a full disk skips the save and the next process
// builds from the index as before.
func saveCatalogSidecar(ix *Index, catalog *exportCatalog) {
	key, ok := ix.GraphCacheKey()
	if !ok {
		return
	}
	sc := sidecarCatalog{
		ByID:       catalog.byID,
		ByFullName: catalog.byFullName,
		ImportDirs: catalog.importDirs,
		ByEndpoint: catalog.byEndpoint,
		Files:      catalog.files,
		Names:      catalog.names,
		Records:    make([]sidecarCall, len(catalog.records)),
	}
	for i, record := range catalog.records {
		sc.Records[i] = sidecarCall{From: record.from, To: record.to, Edge: record.edge}
	}
	_ = graphcache.Save(ix.Root, catalogSidecarName, key, &sc)
}
