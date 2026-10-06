package verifycmd

import "testing"

func TestClassifyCommandQuotedOperators(t *testing.T) {
	allowed := []string{
		`go test ./internal/setup/ -run 'TestA|TestB'`,
		`go test ./... -run "TestA|TestB" && go vet ./...`,
		`go test -run 'a;b' ./...`,
	}
	for _, c := range allowed {
		if err := classifyCommand(c, allToolchains); err != nil {
			t.Errorf("classifyCommand(%q) = %v, want allowed", c, err)
		}
	}

	refused := []string{
		`go test ./... | tail -5`,
		`go test ./... -run 'open`,
		`go test ./... -run "$(whoami)"`,
		"go test ./... -run \"`id`\"",
		`go test ./... ; rm -rf /`,
		`go test ./... > out.txt`,
		`go test ./... -run 'a' | cat`,
	}
	for _, c := range refused {
		if err := classifyCommand(c, allToolchains); err == nil {
			t.Errorf("classifyCommand(%q) allowed, want refused", c)
		}
	}
}
