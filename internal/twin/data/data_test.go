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

// E4: GORM (Go struct tag), JPA (Java @Table), and sqlx (Go db tag) ORM
// patterns must all surface as table nodes wired to the default database —
// previously only SQLAlchemy was covered.
func TestExtractORMGormJPAAndSqlx(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "models.go"), []byte(`
package models

type User struct {
	ID uint `+"`"+`gorm:"table:users"`+"`"+`
}
type Account struct {
	ID uint `+"`"+`db:"accounts"`+"`"+`
}
`), 0644)
	os.WriteFile(filepath.Join(dir, "Entities.java"), []byte(`
@Entity
@Table(name = "orders")
public class Order { }
`), 0644)

	e := New(dir)
	nodes, edges, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == "table" {
			tables[n.Label] = true
		}
	}
	for _, want := range []string{"users", "accounts", "orders"} {
		if !tables[want] {
			t.Errorf("missing table %q in %v", want, tables)
		}
	}
	// Every table hangs off the "default" database via a contains edge.
	contains := 0
	for _, ed := range edges {
		if ed.Kind == "contains" && ed.From == "database:default" {
			contains++
		}
	}
	if contains != 3 {
		t.Errorf("contains edges from default DB = %d, want 3", contains)
	}
}

// E4: CREATE TABLE with IF NOT EXISTS / backticks and a quoted schema name.
func TestExtractSQLQuotedAndIfNotExists(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "migrations"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "migrations", "002.sql"), []byte(`
CREATE TABLE IF NOT EXISTS `+"`"+`user_profiles`+"`"+` (id INT PRIMARY KEY);
CREATE TABLE "audit_log" (id INT PRIMARY KEY);
`), 0644)

	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == "table" {
			tables[n.Label] = true
		}
	}
	for _, want := range []string{"user_profiles", "audit_log"} {
		if !tables[want] {
			t.Errorf("missing table %q in %v", want, tables)
		}
	}
}
