package eventbus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Len() counts ACTIVE SUBSCRIPTIONS (its documented contract), not published
// events. Publishing must never change it.
func TestLen(t *testing.T) {
	b := New()
	if got := b.Len(); got != 0 {
		t.Errorf("fresh bus Len() = %d, want 0", got)
	}
	un1 := b.Subscribe(IncidentCreated, func(Event) {})
	un2 := b.Subscribe(PRCreated, func(Event) {})
	un3 := b.Subscribe("", func(Event) {})
	if got := b.Len(); got != 3 {
		t.Errorf("after 3 subscribes Len() = %d, want 3", got)
	}
	b.Publish(Event{Kind: IncidentCreated})
	b.Publish(Event{Kind: TaskCreated})
	if got := b.Len(); got != 3 {
		t.Errorf("after publishing Len() = %d, want 3 (subscriptions unchanged)", got)
	}
	un1()
	if got := b.Len(); got != 2 {
		t.Errorf("after 1 unsubscribe Len() = %d, want 2", got)
	}
	un1() // idempotent
	if got := b.Len(); got != 2 {
		t.Errorf("double unsubscribe Len() = %d, want 2", got)
	}
	un2()
	un3()
	if got := b.Len(); got != 0 {
		t.Errorf("after all unsubscribes Len() = %d, want 0", got)
	}
}

func TestHistory_FilteredByKind(t *testing.T) {
	b := New()
	b.Publish(Event{Kind: IncidentCreated, Subject: "i1"})
	b.Publish(Event{Kind: TaskCreated, Subject: "t1"})
	b.Publish(Event{Kind: IncidentCreated, Subject: "i2"})
	b.Publish(Event{Kind: DeploymentStarted, Subject: "d1"})
	b.Publish(Event{Kind: IncidentCreated, Subject: "i3"})

	h := b.History(IncidentCreated)
	if len(h) != 3 {
		t.Fatalf("History(IncidentCreated) len = %d, want 3", len(h))
	}
	for i, ev := range h {
		if ev.Kind != IncidentCreated {
			t.Errorf("h[%d].Kind = %q, want %q", i, ev.Kind, IncidentCreated)
		}
	}
	if h[0].Subject != "i1" || h[1].Subject != "i2" || h[2].Subject != "i3" {
		t.Errorf("History(IncidentCreated) = %q,%q,%q, want i1,i2,i3 (oldest first)",
			h[0].Subject, h[1].Subject, h[2].Subject)
	}
	if got := b.History(TaskCreated); len(got) != 1 || got[0].Subject != "t1" {
		t.Errorf("History(TaskCreated) = %+v, want [t1]", got)
	}
	if got := b.History(PRCreated); len(got) != 0 {
		t.Errorf("History(PRCreated) len = %d, want 0", len(got))
	}
	if all := b.History(""); len(all) != 5 {
		t.Errorf("History(\"\") len = %d, want 5", len(all))
	}
}

func TestReplay_MissingFile(t *testing.T) {
	b := New()
	if _, err := b.Replay(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Error("expected error replaying a nonexistent file")
	}
}

func TestReplay_CorruptFile(t *testing.T) {
	b := New()
	var mu sync.Mutex
	var got []string
	b.Subscribe(IncidentCreated, func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ev.Subject)
	})
	p := filepath.Join(t.TempDir(), "hist.jsonl")
	content := "{\"Kind\":\"incident.created\",\"Subject\":\"good\",\"EventVersion\":1,\"OccurredAt\":\"2024-01-01T00:00:00Z\"}\n" +
		"{this is not valid json\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := b.Replay(p)
	if err == nil {
		t.Fatal("expected error replaying a corrupt file")
	}
	if n != 1 {
		t.Errorf("replayed %d events before corruption, want 1", n)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "good" {
		t.Errorf("delivered = %v, want [good]", got)
	}
}

func TestEnablePersistence_Idempotent(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.jsonl")
	p2 := filepath.Join(dir, "b.jsonl")
	b := New()
	b.EnablePersistence(p1)
	b.EnablePersistence(p1) // re-enable: must not panic or duplicate
	b.Publish(Event{Kind: IncidentCreated, Subject: "x"})
	b.Publish(Event{Kind: TaskCreated, Subject: "y"})
	b.EnablePersistence(p2)
	b.Publish(Event{Kind: PRCreated, Subject: "z"})

	lines1 := readLines(t, p1)
	if len(lines1) != 2 {
		t.Fatalf("%s has %d lines, want 2", p1, len(lines1))
	}
	// The two events persisted to p1 must be distinct (no duplicates).
	subjects := map[string]bool{}
	for _, ln := range lines1 {
		var ev Event
		if err := json.Unmarshal(ln, &ev); err != nil {
			t.Fatalf("bad persisted line %q: %v", ln, err)
		}
		subjects[ev.Subject] = true
	}
	if !subjects["x"] || !subjects["y"] || len(subjects) != 2 {
		t.Errorf("persisted subjects = %v, want exactly {x, y}", subjects)
	}
	// Switching paths must not retroactively duplicate into the old file.
	if lines2 := readLines(t, p2); len(lines2) != 1 {
		t.Errorf("%s has %d lines, want 1", p2, len(lines2))
	}
}

func TestDeadLetter_NoSubscriber(t *testing.T) {
	b := New()
	var mu sync.Mutex
	var calls int
	b.Subscribe(IncidentCreated, func(Event) {
		mu.Lock()
		calls++
		mu.Unlock()
		panic("boom") // handler exhausts retries → dead-letter path
	})
	// No SubscribeDeadLetter registered: the dead-lettered event must be
	// dropped silently — no panic escapes, bus stays healthy.
	b.Publish(Event{Kind: IncidentCreated, Subject: "dl-1"})
	b.Flush()
	if calls != 1 {
		t.Errorf("handler calls = %d, want 1", calls)
	}
	if got := b.Len(); got != 1 {
		t.Errorf("Len() = %d, want 1 (subscription intact)", got)
	}
	if h := b.History(IncidentCreated); len(h) != 1 {
		t.Errorf("History(IncidentCreated) len = %d, want 1", len(h))
	}
	// A subsequent healthy publish still delivers.
	var got []string
	var mu2 sync.Mutex
	b.Subscribe(PRCreated, func(ev Event) {
		mu2.Lock()
		defer mu2.Unlock()
		got = append(got, ev.Subject)
	})
	b.Publish(Event{Kind: PRCreated, Subject: "ok"})
	b.Flush()
	mu2.Lock()
	defer mu2.Unlock()
	if len(got) != 1 || got[0] != "ok" {
		t.Errorf("delivered = %v, want [ok]", got)
	}
}

func TestMaxHistoryPayloadSize_Boundary(t *testing.T) {
	b := New()
	// A string payload of N bytes serializes to N+2 bytes (quotes). So:
	// max-2 → exactly 4 KiB serialized (retained), max-1 → 4 KiB + 1 byte
	// (withheld from history).
	exact := strings.Repeat("x", maxHistoryPayloadSize-2)
	over := strings.Repeat("y", maxHistoryPayloadSize-1)
	if n := len(mustJSON(t, exact)); n != maxHistoryPayloadSize {
		t.Fatalf("exact payload serializes to %d bytes, want %d", n, maxHistoryPayloadSize)
	}
	b.Publish(Event{Kind: IncidentCreated, Subject: "exact", Payload: exact})
	b.Publish(Event{Kind: TaskCreated, Subject: "over", Payload: over})
	h := b.History("")
	if len(h) != 2 {
		t.Fatalf("history len = %d, want 2", len(h))
	}
	if h[0].Payload == nil {
		t.Error("payload of exactly 4 KiB was dropped from history")
	}
	if h[1].Payload != nil {
		t.Error("payload over 4 KiB must be withheld from history")
	}
}

func readLines(t *testing.T, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return splitLines(data)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
