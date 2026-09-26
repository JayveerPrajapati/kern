package main

import (
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"os"
	"strings"
)

func runTeam(rest []string) {
	f, _ := parseFlagsOrDie(rest)
	root := projectRoot(f)
	text, err := renderTeamText(root)
	if err != nil {
		fatal("team: %v", err)
	}
	fmt.Print(text)

}

// runWorkflow runs an intent through the agent team workflow.
func runWorkflow(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.task != "" {
		text, err := runWorkflowResumeCLI(root, f.task)
		if err != nil {
			// Render the task state first (it explains why the transition
			// was invalid), then fail loudly — an invalid transition is a
			// failure, not a normal state (exit 1, not 0).
			fmt.Print(text)
			fatal("workflow resume: %v", err)
		}
		fmt.Print(text)
		return
	}
	intent := strings.Join(args, " ")
	if intent == "" {
		fatalUsage("usage: kern workflow <intent> [--task TASK_ID] [--root ROOT]")
	}
	text, err := runWorkflowCLI(root, intent)
	if err != nil {
		fmt.Print(text)
		fatal("workflow: %v", err)
	}
	fmt.Print(text)
}

func runLoop(cmd string, rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern %s <intent> [--level L0..L5] [--root ROOT]", cmd)
	}
	// --mode observe (default) is the current read-only no-op stage loop;
	// --mode autonomous is the former kern_do behavior (LLM coder + planner
	// wired as the loop's default stage handlers, default level L2) — the
	// CLI mirror of kern_loop mode=autonomous (surface consolidation T2b).
	switch f.mode {
	case "", "observe":
	case "autonomous":
	default:
		fatalUsage("loop: unknown --mode %q (valid modes: observe, autonomous)", f.mode)
	}
	intent := args[0]
	if f.schedule != "" {
		// --schedule: closed-loop runs on a cron cadence (Feature Batch I).
		runLoopScheduled(cmd, f, root, intent)
		return
	}
	if f.mode == "autonomous" {
		// runDo probes the LLM provider up front and routes through
		// TaskService.RunDo (default level L2 when --level is absent).
		text, err := runDo(root, f.level, intent)
		if err != nil {
			fmt.Print(text)
			fatal("Loop: %v", err)
		}
		fmt.Print(text)
		return
	}
	text, err := runLoopCLI(root, f.level, intent)
	if err != nil {
		fmt.Print(text)
		fatal("Loop: %v", err)
	}
	fmt.Print(text)

}

// listIncidents renders the persisted incident history (newest first) —
// the Persona 3 SRE ask: browse past incidents without JSON-on-CLI intake.
func listIncidents(root string, jsonOut bool) {
	store := incident.NewStore(root)
	list, err := store.List()
	if err != nil {
		fatal("list incidents: %v", err)
	}
	if jsonOut {
		b, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			fatal("list incidents: %v", err)
		}
		fmt.Println(string(b))
		return
	}
	if len(list) == 0 {
		fmt.Println("no incidents recorded")
		return
	}
	fmt.Printf("incidents (%d, newest first):\n", len(list))
	for _, inc := range list {
		title := inc.Title
		if title == "" {
			title = "(no title)"
		}
		fmt.Printf("  %s  [%s/%s]  %s\n", inc.ID, inc.Severity, inc.Status, title)
		if inc.AffectedService != "" {
			fmt.Printf("    service: %s\n", inc.AffectedService)
		}
		if inc.RootCause != nil && inc.RootCause.Summary != "" {
			fmt.Printf("    root cause: %s\n", inc.RootCause.Summary)
		}
		if inc.FixDescription != "" {
			fmt.Printf("    fix: %s\n", inc.FixDescription)
		}
		if inc.PRURL != "" {
			fmt.Printf("    pr: %s\n", inc.PRURL)
		}
	}
}

func runIncident(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)

	// `kern incident list` browses the persisted incident history instead
	// of ingesting a new alert — the SRE on-call ask (Persona 3).
	if len(args) > 0 && args[0] == "list" {
		listIncidents(root, f.json)
		return
	}

	// Heal-playbook store (Feature Batch D): list or add without an alert.
	if f.listPlaybooks {
		list, err := incident.ListPlaybooks(root)
		if err != nil {
			fatal("list playbooks: %v", err)
		}
		if len(list) == 0 {
			fmt.Println("no playbooks stored")
			return
		}
		fmt.Printf("playbooks (%d):\n", len(list))
		for _, pb := range list {
			fmt.Printf("  %s\n", pb.Signature)
			for _, s := range pb.Steps {
				fmt.Printf("    step: %s\n", s)
			}
			if pb.Source != "" {
				fmt.Printf("    source: %s\n", pb.Source)
			}
		}
		return
	}
	if f.runbook != "" {
		var pb incident.Playbook
		rbText := f.runbook
		// Accept a file path to the runbook JSON as well as inline JSON.
		if _, serr := os.Stat(rbText); serr == nil {
			if b, rerr := os.ReadFile(rbText); rerr == nil {
				rbText = string(b)
			}
		}
		if err := json.Unmarshal([]byte(rbText), &pb); err != nil {
			fatal("invalid runbook JSON (pass JSON inline or a file path): %v", err)
		}
		if err := incident.AddPlaybook(root, pb); err != nil {
			fatal("add playbook: %v", err)
		}
		fmt.Printf("added playbook: %s (%d steps)\n", pb.Signature, len(pb.Steps))
		return
	}

	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern incident list | <alert-json> [snapshot-json] [--root ROOT] [--correlate] [--json] [--runbook JSON] [--list-playbooks]")
	}
	var al domain.Alert
	alertText := args[0]
	// Accept a file path to the alert JSON as well as inline JSON — the
	// previous behavior parsed the literal argument and produced a cryptic
	// "invalid character '/'" for paths.
	if _, serr := os.Stat(alertText); serr == nil {
		if b, rerr := os.ReadFile(alertText); rerr == nil {
			alertText = string(b)
		}
	}
	if err := json.Unmarshal([]byte(alertText), &al); err != nil {
		fatal("invalid alert JSON (pass JSON inline or a file path): %v", err)
	}
	// Route through TaskService.InvestigateIncident for the full lifecycle.
	p, err := app.New(root)
	if err != nil {
		fatal("could not load project: %v — run kern index first", err)
	}
	if len(args) > 1 && args[1] != "" {
		store, err := runtime.ParseSnapshot([]byte(args[1]))
		if err != nil {
			fatal("invalid snapshot JSON: %v", err)
		}
		p.WithRuntimeSource(store)
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())

	// --correlate: run the incident→twin→code correlation engine and render
	// the correlation report (Feature Batch D) instead of the full pipeline.
	if f.correlate {
		t, corr, text, err := ts.CorrelateCode(al)
		if err != nil {
			fatal("incident correlate: %v", err)
		}
		fmt.Print(text)
		fmt.Printf("[task: %s — state: %s — incident: %s]\n", t.ID, t.State, corr.Alert.ID)
		return
	}

	t, inc, text, err := ts.InvestigateIncident(al)
	if err != nil {
		fatal("incident: %v", err)
	}
	fmt.Print(text)
	fmt.Printf("[task: %s — state: %s — incident: %s]\n", t.ID, t.State, inc.ID)
}

func runDocs(rest []string) {
	f, args := parseFlagsOrDie(rest)
	sub := ""
	if len(args) > 0 && (args[0] == "index" || args[0] == "clear" || args[0] == "fetch") {
		sub = args[0]
		args = args[1:]
	}
	root := "."
	query := ""
	if sub == "" && len(args) > 0 {
		query = args[0]
		args = args[1:]
	}
	if sub == "fetch" {
		if len(args) == 0 {
			fatalUsage("usage: kern docs fetch <url> [name] [root]")
		}
		rawURL := args[0]
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		if len(args) > 2 {
			root = args[2]
		}
		if f.root != "" {
			root = f.root
		}
		docsFetchCore(rawURL, name, root, f.semantic)
		return
	}
	if len(args) > 0 {
		root = args[0]
	}
	if f.root != "" {
		root = f.root
	}
	switch sub {
	case "index":
		var ix *docsearch.Index
		var err error
		if f.semantic {
			client := llm.NewEmbedder()
			if !client.Available() {
				fatal("ollama not reachable (semantic index requires a local Ollama); run without --semantic for deterministic indexing")
			}
			if !client.HasEmbeddingModel() {
				fatal("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			docsearch.SemanticEmbedder = client
			ix, err = docsearch.IndexDirSemantic(root, client)
		} else {
			ix, err = docsearch.IndexDir(root)
		}
		if err != nil {
			fatal("docs index: %v", err)
		}
		if err := ix.Save(); err != nil {
			fatal("docs index: %v", err)
		}
		fmt.Printf("indexed %d chunks from %s\n", len(ix.Docs), root)
	case "clear":
		_ = os.RemoveAll(cache.Path("data", "docs"))
		_ = os.RemoveAll(cache.Path("data", "docs-fetch"))
		fmt.Println("cleared document index and fetched-doc cache")
	default:
		if query == "" {
			fatalUsage("usage: kern docs <query> [root] [--root ROOT] [--limit N] | kern docs index [root] [--semantic] | kern docs clear")
		}
		docsSearchCore(query, root, f.limit)
	}

}
