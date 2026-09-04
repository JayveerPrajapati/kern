package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestExtractGinRoutes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`
package main
func main() {
	r.GET("/users", listUsers)
	r.POST("/users", createUser)
	r.DELETE("/users/:id", deleteUser)
}
`), 0644)

	e := New(dir)
	nodes, edges, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(nodes))
	}
	if nodes[0].API.Method != "GET" || nodes[0].API.Path != "/users" {
		t.Errorf("first API = %+v", nodes[0].API)
	}
	if nodes[0].API.Framework != "gin" {
		t.Errorf("framework = %q, want gin", nodes[0].API.Framework)
	}
	if nodes[0].API.Symbol != "listUsers" {
		t.Errorf("handler symbol = %q, want listUsers", nodes[0].API.Symbol)
	}
	// Verify edges include implements and defined_in.
	hasImplements, hasDefinedIn := false, false
	for _, e := range edges {
		switch e.Kind {
		case "implements":
			hasImplements = true
		case "defined_in":
			hasDefinedIn = true
		}
	}
	if !hasImplements {
		t.Error("no implements edges")
	}
	if !hasDefinedIn {
		t.Error("no defined_in edges")
	}
}

func TestExtractExpressRoutes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.js"), []byte(`
const express = require('express');
const app = express();
app.get("/users", listUsers);
router.post("/users", async (req, res) => createUser(req, res));
app.put('/users/:id', updateUser);
`), 0644)

	e := New(dir)
	nodes, edges, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(nodes))
	}
	if nodes[0].API.Method != "GET" || nodes[0].API.Path != "/users" {
		t.Errorf("first API = %+v", nodes[0].API)
	}
	if nodes[1].API.Method != "POST" || nodes[1].API.Path != "/users" {
		t.Errorf("second API = %+v", nodes[1].API)
	}
	if nodes[2].API.Method != "PUT" {
		t.Errorf("third API method = %q, want PUT", nodes[2].API.Method)
	}
	hasImplements := false
	for _, e := range edges {
		if e.Kind == "implements" {
			hasImplements = true
		}
	}
	if !hasImplements {
		t.Error("no implements edges")
	}
}

func TestExtractFlaskRoutes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.py"), []byte(`
from flask import Flask
app = Flask(__name__)

@app.route("/users")
def list_users():
    return "ok"

@app.route("/users/<int:id>")
def get_user(id):
    return "ok"
`), 0644)

	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	if nodes[0].API.Path != "/users" || nodes[0].API.Framework != "flask" {
		t.Errorf("first API = %+v", nodes[0].API)
	}
}

func TestExtractFastAPIRoutes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.py"), []byte(`
from fastapi import FastAPI
app = FastAPI()

@app.get("/health")
def health():
    return "ok"

@app.post("/items")
def create_item():
    return "ok"
`), 0644)

	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	if nodes[0].API.Method != "GET" || nodes[0].API.Path != "/health" {
		t.Errorf("first API = %+v", nodes[0].API)
	}
	if nodes[0].API.Framework != "fastapi" {
		t.Errorf("framework = %q, want fastapi", nodes[0].API.Framework)
	}
}

func TestExtractNetHTTPRoutes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`
package main
import "net/http"
func main() {
	http.HandleFunc("/users", listUsers)
	http.HandleFunc("/health", health)
}
`), 0644)

	e := New(dir)
	nodes, edges, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	if nodes[0].API.Path != "/users" || nodes[0].API.Symbol != "listUsers" {
		t.Errorf("first API = %+v", nodes[0].API)
	}
	if !hasImplements(edges) {
		t.Error("no implements edges")
	}
}

func TestExtractIgnoresVendorDirs(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "node_modules"), 0755)
	os.WriteFile(filepath.Join(dir, "node_modules", "app.js"), []byte(`
app.get("/hidden", handler);
`), 0644)
	os.WriteFile(filepath.Join(dir, "app.js"), []byte(`
app.get("/visible", handler);
`), 0644)

	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1 (ignoring node_modules)", len(nodes))
	}
	if !strings.Contains(nodes[0].API.Path, "visible") {
		t.Errorf("unexpected node = %+v", nodes[0].API)
	}
}

func hasImplements(edges []domain.Edge) bool {
	for _, e := range edges {
		if e.Kind == "implements" {
			return true
		}
	}
	return false
}
