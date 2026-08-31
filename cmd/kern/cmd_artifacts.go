package main

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// runArtifacts lists task artifacts or all known artifacts.
func runArtifacts(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}

	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())

	if len(args) >= 1 && args[0] != "" {
		// List artifacts for a specific task.
		taskID := args[0]
		arts, err := ts.Artifacts().GetByTask(taskID)
		if err != nil {
			fatal("%v", err)
		}
		if len(arts) == 0 {
			fmt.Printf("no artifacts for task %s\n", taskID)
			return
		}
		fmt.Printf("artifacts for task %s (%d):\n", taskID, len(arts))
		for _, a := range arts {
			parent := "(root)"
			if a.ParentArtifactID != "" {
				parent = a.ParentArtifactID
			}
			fmt.Printf("  %s\n", a.ID)
			fmt.Printf("    kind: %s\n", a.Kind)
			fmt.Printf("    created_by: %s\n", a.CreatedBy)
			fmt.Printf("    parent: %s\n", parent)
			fmt.Printf("    scope: %s\n", a.Scope)
			fmt.Printf("    provenance: %s\n", a.Provenance)
			fmt.Printf("    created_at: %s\n", a.CreatedAt.Format("2006-01-02 15:04:05"))
		}
		return
	}

	// List all artifacts.
	arts, err := ts.Artifacts().List()
	if err != nil {
		fatal("%v", err)
	}
	if len(arts) == 0 {
		fmt.Println("no artifacts")
		return
	}
	fmt.Printf("artifacts (%d):\n", len(arts))
	for _, a := range arts {
		parent := "(root)"
		if a.ParentArtifactID != "" {
			parent = a.ParentArtifactID
		}
		fmt.Printf("  %s  kind=%s  task=%s  parent=%s\n", a.ID, a.Kind, a.TaskID, parent)
	}
}
