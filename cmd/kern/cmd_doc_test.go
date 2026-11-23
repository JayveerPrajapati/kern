package main

import (
	"testing"
)

func TestDocsFetchCoreDeniesUnapprovedHost(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("docsFetchCore returned instead of failing closed")
		}
	}()

	docsFetchCore("https://example.com/unapproved", "", t.TempDir(), false)
}
