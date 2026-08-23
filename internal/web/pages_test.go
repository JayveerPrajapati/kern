package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
)

// TestAgentsPage asserts /agents renders the specialist roster and the shared
// top navigation.
func TestAgentsPage(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/agents")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"<title>Agents", "Specialists", "topnav", `href="/agents"`, "planner", "capabilities"} {
		if !strings.Contains(body, want) {
			t.Fatalf("agents page missing %q", want)
		}
	}
}

// TestTasksPage asserts /tasks renders the efficiency summary and the shared
// top navigation, even with an empty task roster.
func TestTasksPage(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/tasks")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"<title>Tasks", "Summary", "topnav", `href="/tasks"`, "avg token reduction"} {
		if !strings.Contains(body, want) {
			t.Fatalf("tasks page missing %q", want)
		}
	}
}

// TestTasksPageWithTask seeds a task and asserts its row renders with a link to
// /task/{id} and a projected efficiency row.
func TestTasksPageWithTask(t *testing.T) {
	app := newTestApp(t)
	tk := agent.NewTask("code", "fix the checkout 500")
	if err := app.tasks.SubmitTask(tk); err != nil {
		t.Fatal(err)
	}
	rec := get(t, app, "/tasks")
	body := rec.Body.String()
	if !strings.Contains(body, tk.ID) {
		t.Fatalf("tasks page missing task id %q", tk.ID)
	}
	if !strings.Contains(body, `href="/task/`+tk.ID+`"`) {
		t.Fatalf("tasks page missing detail link for %q", tk.ID)
	}
}

// TestDashboardNav asserts the dashboard now carries the top navigation linking
// to all three pages.
func TestDashboardNav(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/")
	body := rec.Body.String()
	for _, want := range []string{`href="/"`, `href="/tasks"`, `href="/agents"`, "topnav"} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard missing nav link %q", want)
		}
	}
}
