package memory

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// QA 5.4 (read-your-writes): the MCP server dispatches tools/call requests
// concurrently, so a kern_memory add followed by a recall in the same session
// could miss the just-added lesson: the recall's unlocked read landed before
// the add's atomic rename. These tests pin the fix — reads serialize with
// writers.

// TestAddThenRecallSeesWrite pins the user-visible contract: after a
// completed AddExplicit, a recall for the same session must find the lesson.
func TestAddThenRecallSeesWrite(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	lesson := "the verify API runs a full build verdict"
	if _, err := AddExplicit(root, lesson); err != nil {
		t.Fatalf("AddExplicit: %v", err)
	}
	got := Recall(root, "how does the verify api build verdict work", 5)
	if len(got) == 0 {
		t.Fatal("recall must see the just-added lesson")
	}
	if got[0].Text != lesson {
		t.Fatalf("recall returned %q, want %q", got[0].Text, lesson)
	}
}

// TestListBlocksWhileWriterHoldsLock pins the serialization deterministically
// (no timing): List must take the same cross-process lock the writers hold.
// While the lock is held, List must block; once released it returns and sees
// the write. Without the fix List returns immediately (and the add→recall
// race in the concurrent server is unfixed).
func TestListBlocksWhileWriterHoldsLock(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	fl, err := cache.LockFile(Path(root)) // simulate an in-flight writer
	if err != nil {
		t.Fatalf("LockFile: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = List(root) // must block until the writer releases
	}()
	select {
	case <-done:
		t.Fatal("List returned while a writer held the store lock — read is not serialized with writes")
	case <-time.After(300 * time.Millisecond):
		// blocked as required
	}
	fl.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("List did not return after the writer released the lock")
	}
}

// TestConcurrentAddRecallConsistent runs the actual race shape: adds and
// recalls dispatched concurrently (the MCP server's per-call goroutines). With
// reads serialized against writes, every lesson whose add completed before its
// recall is visible; at the end ALL added lessons are recallable.
func TestConcurrentAddRecallConsistent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	const lessons = 20
	var wg sync.WaitGroup
	for i := 0; i < lessons; i++ {
		wg.Add(2)
		text := "concurrent lesson number " + strings.Repeat("x", i) + " alpha"
		go func() {
			defer wg.Done()
			_, _ = AddExplicit(root, text)
		}()
		go func() {
			defer wg.Done()
			_ = Recall(root, "concurrent lesson alpha", 5)
		}()
	}
	wg.Wait()
	for i := 0; i < lessons; i++ {
		text := "concurrent lesson number " + strings.Repeat("x", i) + " alpha"
		got := Recall(root, text, 5)
		found := false
		for _, e := range got {
			if e.Text == text {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("lesson %q added before the wait is not recallable afterwards", text)
		}
	}
}

// TestTypedStoreReadSerializedWithWriters pins the same serialization for the
// typed store: CurrentMemories must block while an Add holds the typed-store
// lock, so buddy's "Project memory" read cannot race an in-flight add.
func TestTypedStoreReadSerializedWithWriters(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	st := NewMemoryStore(root)
	fl, err := cache.LockFile(st.path)
	if err != nil {
		t.Fatalf("LockFile: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = st.CurrentMemories("")
	}()
	select {
	case <-done:
		t.Fatal("CurrentMemories returned while a writer held the typed-store lock")
	case <-time.After(300 * time.Millisecond):
	}
	fl.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CurrentMemories did not return after the writer released the lock")
	}
}
