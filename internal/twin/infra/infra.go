package infra

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/twin/ids"
)

type Extractor struct {
	root string
}

func New(root string) *Extractor {
	return &Extractor{root: root}
}

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
		base := d.Name()
		ext := filepath.Ext(path)
		switch {
		case base == "docker-compose.yml" || base == "docker-compose.yaml" || strings.HasPrefix(base, "docker-compose."):
			n, ed := e.extractDockerCompose(path)
			nodes, edges = append(nodes, n...), append(edges, ed...)
		case ext == ".tf":
			n, ed := e.extractTerraform(path)
			nodes, edges = append(nodes, n...), append(edges, ed...)
		case ext == ".yaml" || ext == ".yml":
			n, ed := e.extractK8s(path)
			nodes, edges = append(nodes, n...), append(edges, ed...)
		case base == "Chart.yaml":
			n := e.extractHelmChart(path)
			nodes = append(nodes, n...)
		}
		return nil
	})
	return nodes, edges, err
}

func (e *Extractor) extractDockerCompose(path string) ([]domain.Node, []domain.Edge) {
	relPath, _ := filepath.Rel(e.root, path)
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var nodes []domain.Node
	var edges []domain.Edge
	scanner := bufio.NewScanner(f)
	inServices := false
	indent := 0
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "services:" {
			inServices = true
			indent = len(line) - len(strings.TrimLeft(line, " "))
			continue
		}
		if inServices {
			curIndent := len(line) - len(strings.TrimLeft(line, " "))
			if curIndent <= indent && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				inServices = false
				continue
			}
			if curIndent == indent+2 && strings.HasSuffix(trimmed, ":") {
				svcName := strings.TrimSuffix(trimmed, ":")
				svcID := "service:" + ids.Escape(svcName)
				nodes = append(nodes, domain.Node{
					ID:      svcID,
					Kind:    "service",
					Label:   svcName,
					Service: &domain.Service{Name: svcName, Type: "container"},
				})
				edges = append(edges, domain.Edge{From: svcID, To: "file:" + relPath, Kind: "defined_in"})
			}
		}
	}
	return nodes, edges
}

var k8sKindRe = regexp.MustCompile(`^kind:\s*(\w+)`)
var k8sNameRe = regexp.MustCompile(`^\s+name:\s*(\S+)`)

func (e *Extractor) extractK8s(path string) ([]domain.Node, []domain.Edge) {
	relPath, _ := filepath.Rel(e.root, path)
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var nodes []domain.Node
	var edges []domain.Edge
	scanner := bufio.NewScanner(f)
	var currentKind string
	for scanner.Scan() {
		line := scanner.Text()
		if km := k8sKindRe.FindStringSubmatch(line); km != nil {
			currentKind = km[1]
			continue
		}
		if currentKind != "" {
			if nm := k8sNameRe.FindStringSubmatch(line); nm != nil {
				name := strings.Trim(nm[1], `"'`)
				svcID := "service:" + ids.Escape(name)
				nodes = append(nodes, domain.Node{
					ID:      svcID,
					Kind:    "service",
					Label:   name,
					Service: &domain.Service{Name: name, Type: "k8s-" + strings.ToLower(currentKind)},
				})
				edges = append(edges, domain.Edge{From: svcID, To: "file:" + relPath, Kind: "defined_in"})
				currentKind = ""
			}
		}
	}
	return nodes, edges
}

var terraformResourceRe = regexp.MustCompile(`resource\s+"(\w+)"\s+"(\w+)"`)

func (e *Extractor) extractTerraform(path string) ([]domain.Node, []domain.Edge) {
	relPath, _ := filepath.Rel(e.root, path)
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var nodes []domain.Node
	var edges []domain.Edge
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		matches := terraformResourceRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		resType, resName := matches[1], matches[2]
		svcType := "terraform"
		if strings.HasPrefix(resType, "aws_") {
			svcType = "aws"
		}
		if strings.HasPrefix(resType, "google_") {
			svcType = "gcp"
		}
		if strings.HasPrefix(resType, "azurerm_") {
			svcType = "azure"
		}
		svcID := "service:" + ids.Escape(resName)
		nodes = append(nodes, domain.Node{
			ID:      svcID,
			Kind:    "service",
			Label:   resName,
			Service: &domain.Service{Name: resName, Type: svcType},
		})
		edges = append(edges, domain.Edge{From: svcID, To: "file:" + relPath, Kind: "defined_in"})
	}
	return nodes, edges
}

func (e *Extractor) extractHelmChart(path string) []domain.Node {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "name:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			name = strings.Trim(name, `"'`)
			return []domain.Node{{
				ID:      "service:" + ids.Escape(name),
				Kind:    "service",
				Label:   name,
				Service: &domain.Service{Name: name, Type: "helm"},
			}}
		}
	}
	return nil
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
