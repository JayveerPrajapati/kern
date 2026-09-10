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

// E4: a Helm chart (Chart.yaml) surfaces as a helm-typed service node.
func TestExtractHelmChart(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "charts", "api", "templates"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "charts", "api", "Chart.yaml"), []byte(`
apiVersion: v2
name: api-server
version: 0.1.0
`), 0644)

	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nodes {
		if n.Kind == "service" && n.Service != nil && n.Service.Type == "helm" && n.Service.Name == "api-server" {
			found = true
		}
	}
	if !found {
		t.Errorf("helm service api-server not extracted: %v", nodes)
	}
}

// E4: docker-compose with multiple services and the dashed filename variant
// (docker-compose.prod.yml) must both be picked up.
func TestExtractDockerComposeVariantAndServices(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "docker-compose.prod.yml"), []byte(`
services:
  web:
    image: nginx:1.25
  db:
    image: postgres:16
`), 0644)

	e := New(dir)
	nodes, _, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	services := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == "service" && n.Service != nil && n.Service.Type == "container" {
			services[n.Service.Name] = true
		}
	}
	if !services["web"] || !services["db"] {
		t.Errorf("docker-compose services = %v, want web+db container services", services)
	}
}
