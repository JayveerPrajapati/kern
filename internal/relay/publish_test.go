//go:build !windows

package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// TestPublishPersistedLiveAndDurable verifies the dual leg: with a relay
// owner running, PublishPersisted both appends to events.jsonl and reaches
// the owner's bus live (the SetPublisher callback).
func TestPublishPersistedLiveAndDurable(t *testing.T) {
	root := t.TempDir()
	published := make(chan eventbus.Event, 4)
	srv, err := Start(root)
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	defer srv.Close()
	srv.SetPublisher(func(e eventbus.Event) { published <- e })

	evts := []eventbus.Event{{ID: "g-live-1", Kind: eventbus.ArchitectureViolation, Source: "guard", Subject: "DoWork"}}
	PublishPersisted(root, evts)

	select {
	case e := <-published:
		if e.ID != "g-live-1" || e.Kind != eventbus.ArchitectureViolation || e.Source != "guard" {
			t.Fatalf("live event mismatch: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay owner never received the live emit")
	}
	data, err := os.ReadFile(filepath.Join(root, ".kern", "events.jsonl"))
	if err != nil {
		t.Fatalf("read events file: %v", err)
	}
	if !strings.Contains(string(data), `"ID":"g-live-1"`) {
		t.Fatalf("durable leg missing, got: %s", string(data))
	}
}

// TestPublishPersistedWithoutRelay: no owner running — the durable leg still
// runs and the missing socket is not an error.
func TestPublishPersistedWithoutRelay(t *testing.T) {
	root := t.TempDir()
	PublishPersisted(root, []eventbus.Event{{ID: "g-norelay", Kind: eventbus.ArchitectureWarning, Source: "guard"}})
	data, err := os.ReadFile(filepath.Join(root, ".kern", "events.jsonl"))
	if err != nil {
		t.Fatalf("read events file: %v", err)
	}
	if !strings.Contains(string(data), `"ID":"g-norelay"`) {
		t.Fatalf("durable leg missing without relay, got: %s", string(data))
	}
}

// TestPublishPersistedEmptyIsNoop: an empty batch touches nothing.
func TestPublishPersistedEmptyIsNoop(t *testing.T) {
	root := t.TempDir()
	PublishPersisted(root, nil)
	if _, err := os.Stat(filepath.Join(root, ".kern", "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("empty batch should not create the events file, stat err=%v", err)
	}
}
