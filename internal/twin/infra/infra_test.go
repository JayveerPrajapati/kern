package infra

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractDockerCompose(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(`
services:
  postgres:
    image: postgres:15
  redis:
    image: redis:7
`), 0644)
	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
}

func TestExtractTerraform(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "aws_instance" "web" { ami = "ami-12345" }
resource "aws_db_instance" "database" { engine = "postgres" }
`), 0644)
	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
}

func TestExtractK8sManifest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deployment.yaml"), []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api-server
spec:
  replicas: 3
`), 0644)
	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	if nodes[0].Service == nil || nodes[0].Service.Name != "api-server" {
		t.Errorf("name = %+v", nodes[0].Service)
	}
}
