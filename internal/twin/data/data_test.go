package data

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractSQLTables(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "migrations"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "migrations", "001.sql"), []byte(`
CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(255));
CREATE TABLE orders (id INT PRIMARY KEY, user_id INT);
`), 0644)

	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	// Should have 1 database + 2 tables = 3 nodes
	tables := 0
	for _, n := range nodes {
		if n.Kind == "table" {
			tables++
		}
	}
	if tables != 2 {
		t.Errorf("tables = %d, want 2", tables)
	}
}

func TestExtractSQLAlchemyModels(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "models.py"), []byte(`
class User:
    __tablename__ = "users"
class Order:
    __tablename__ = "orders"
`), 0644)

	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	tables := 0
	for _, n := range nodes {
		if n.Kind == "table" {
			tables++
		}
	}
	if tables != 2 {
		t.Errorf("tables = %d, want 2", tables)
	}
}
