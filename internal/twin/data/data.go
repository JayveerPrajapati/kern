package data

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/twin/ids"
)

// Extractor scans a repository for database schemas and ORM models.
type Extractor struct {
	root string
}

func New(root string) *Extractor {
	return &Extractor{root: root}
}

// Extract returns Database and Table nodes + edges.
func (e *Extractor) Extract() ([]domain.Node, []domain.Edge, error) {
	var nodes []domain.Node
	var edges []domain.Edge

	err := filepath.WalkDir(e.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if isIgnoreDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		switch ext {
		case ".sql":
			n, ed := e.extractSQL(path)
			nodes, edges = append(nodes, n...), append(edges, ed...)
		case ".go", ".py", ".java":
			n, ed := e.extractORM(path)
			nodes, edges = append(nodes, n...), append(edges, ed...)
		}
		return nil
	})
	return nodes, edges, err
}

// createTableRe matches CREATE TABLE statements.
var createTableRe = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?[\` + "`" + `"]?(\w+)[\` + "`" + `"]?`)

// extractSQL parses SQL files for CREATE TABLE statements.
// Each .sql file is associated with a database (inferred from directory
// name or "default" when unknown). Each CREATE TABLE produces a Table node.
func (e *Extractor) extractSQL(path string) ([]domain.Node, []domain.Edge) {
	relPath, _ := filepath.Rel(e.root, path)

	// Infer database name from parent directory (e.g. migrations/postgres/ → postgres)
	dir := filepath.Dir(relPath)
	dbName := "default"
	if dir != "." && !strings.Contains(dir, "migrations") {
		dbName = filepath.Base(dir)
	}
	dbID := "database:" + ids.Escape(dbName)

	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var nodes []domain.Node
	var edges []domain.Edge

	// Database node (one per unique db name — dedup handled by caller)
	nodes = append(nodes, domain.Node{
		ID:       dbID,
		Kind:     "database",
		Label:    dbName,
		Database: &domain.Database{Name: dbName, Type: "sql"},
	})

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		matches := createTableRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		tableName := matches[1]
		tableID := "table:" + ids.Escape(dbName) + ":" + ids.Escape(tableName)
		nodes = append(nodes, domain.Node{
			ID:    tableID,
			Kind:  "table",
			Label: tableName,
			Table: &domain.Table{Name: tableName, Database: dbName},
		})
		// Edge: database contains table
		edges = append(edges, domain.Edge{From: dbID, To: tableID, Kind: "contains"})
		// Edge: table defined in file
		edges = append(edges, domain.Edge{From: tableID, To: "file:" + relPath, Kind: "defined_in"})
	}
	return nodes, edges
}

// ORM patterns for model/table detection
var (
	// GORM (Go): `gorm:"table:users"` or TableName() method
	gormTableRe = regexp.MustCompile(`gorm:"table:(\w+)"`)
	// SQLAlchemy (Python): __tablename__ = "users"
	sqlalchemyRe = regexp.MustCompile(`__tablename__\s*=\s*['"](\w+)['"]`)
	// JPA (Java): @Table(name = "users")
	jpaTableRe = regexp.MustCompile(`@Table\s*\(\s*(?:name\s*=\s*)?['"](\w+)['"]`)
	// Go struct with db tags: `db:"users"` (sqlx style)
	sqlxTableRe = regexp.MustCompile(`db:"(\w+)"`)
)

func (e *Extractor) extractORM(path string) ([]domain.Node, []domain.Edge) {
	relPath, _ := filepath.Rel(e.root, path)
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var nodes []domain.Node
	var edges []domain.Edge
	dbName := "default" // ORM models default to "default" DB unless config says otherwise
	dbID := "database:" + ids.Escape(dbName)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		for _, re := range []*regexp.Regexp{gormTableRe, sqlalchemyRe, jpaTableRe, sqlxTableRe} {
			matches := re.FindStringSubmatch(line)
			if matches == nil {
				continue
			}
			tableName := matches[1]
			tableID := "table:" + ids.Escape(dbName) + ":" + ids.Escape(tableName)
			nodes = append(nodes, domain.Node{
				ID:    tableID,
				Kind:  "table",
				Label: tableName,
				Table: &domain.Table{Name: tableName, Database: dbName},
			})
			edges = append(edges, domain.Edge{From: dbID, To: tableID, Kind: "contains"})
			edges = append(edges, domain.Edge{From: tableID, To: "file:" + relPath, Kind: "defined_in"})
			break // one table per line
		}
	}
	// Add database node if we found tables
	if len(nodes) > 0 {
		nodes = append([]domain.Node{{
			ID:       dbID,
			Kind:     "database",
			Label:    dbName,
			Database: &domain.Database{Name: dbName, Type: "orm"},
		}}, nodes...)
	}
	return nodes, edges
}

func isIgnoreDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "out", "target",
		".venv", "venv", "__pycache__", ".cache", ".idea", ".vscode",
		".kern", "tmp", "coverage":
		return true
	}
	return false
}
