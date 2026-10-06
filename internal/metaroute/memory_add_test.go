package metaroute_test

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/metaroute"
)

func TestClassifyMetaRequest_MemoryAddRoutes(t *testing.T) {
	for _, in := range []string{
		"remember this lesson: deploy tags come from the release workflow",
		"add lesson: deploy tags come from the release workflow",
		"save to memory: deploy tags come from the release workflow",
		"use kern_memory to add lesson: deploy tags come from the release workflow",
	} {
		tool, args := metaroute.ClassifyMetaRequest(in)
		if tool != "kern_memory" || args["action"] != "add" {
			t.Errorf("%q -> %q %v, want kern_memory add", in, tool, args)
			continue
		}
		if args["lesson"] != "deploy tags come from the release workflow" {
			t.Errorf("%q -> lesson %q", in, args["lesson"])
		}
	}
}

func TestClassifyMetaRequest_MemoryRecallWithoutPayloadStaysRecall(t *testing.T) {
	tool, args := metaroute.ClassifyMetaRequest("remember a lesson learned about the sandbox")
	if tool != "kern_memory" || args["action"] != "recall" {
		t.Errorf("got %q %v, want kern_memory recall", tool, args)
	}
}
