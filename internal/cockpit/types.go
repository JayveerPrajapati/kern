package cockpit

import (
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/gates"
	"github.com/JayveerPrajapati/kern/internal/loop"
)

// Phase represents a lifecycle phase of Governed Autonomous Engineering.
type Phase string

const (
	PhaseIntent   Phase = "INTENT"
	PhasePlan     Phase = "PLAN"
	PhaseCode     Phase = "CODE"
	PhaseFirewall Phase = "FIREWALL"
	PhaseVerify   Phase = "VERIFY"
	PhaseApproval Phase = "APPROVAL"
	PhaseReceipt  Phase = "RECEIPT"
)

// OrderedPhases defines the execution sequence.
var OrderedPhases = []Phase{
	PhaseIntent,
	PhasePlan,
	PhaseCode,
	PhaseFirewall,
	PhaseVerify,
	PhaseApproval,
	PhaseReceipt,
}

// Status represents the state of a phase or gate.
type Status string

const (
	StatusPending   Status = "PENDING"
	StatusRunning   Status = "RUNNING"
	StatusPass      Status = "PASS"
	StatusWarn      Status = "WARN"
	StatusBlock     Status = "BLOCK"
	StatusRepairing Status = "REPAIR"
	StatusSkipped   Status = "SKIP"
)

// PhaseState tracks progress of a single phase.
type PhaseState struct {
	Phase    Phase
	Status   Status
	Started  time.Time
	Duration time.Duration
	Message  string
}

// GateState tracks the real-time status of one Blueprint gate (G0-G29).
type GateState struct {
	Gate        gates.Gate
	Status      Status
	Violations  int
	LastMessage string
}

// State represents the complete cockpit snapshot.
type State struct {
	TaskID          string
	Intent          string
	AutonomyLevel   string
	RepoRoot        string
	WorktreeDir     string
	ActivePhase     Phase
	Phases          map[Phase]*PhaseState
	Gates           map[string]*GateState
	Diff            string
	RepairAttempts  int
	RepairContracts []loop.RepairContract
	TokensSaved     int
	TokensUsed      int
	CostDollars     float64
	ApprovalNeeded  bool
	ApprovalReason  string
	Approved        bool
	Completed       bool
	Success         bool
	Error           string
}

// NewInitialState creates an empty State initialized with all 30 Blueprint gates.
func NewInitialState(taskID, intent, repoRoot string) *State {
	s := &State{
		TaskID:        taskID,
		Intent:        intent,
		AutonomyLevel: "L3",
		RepoRoot:      repoRoot,
		ActivePhase:   PhaseIntent,
		Phases:        make(map[Phase]*PhaseState),
		Gates:         make(map[string]*GateState),
	}

	for _, p := range OrderedPhases {
		s.Phases[p] = &PhaseState{
			Phase:  p,
			Status: StatusPending,
		}
	}

	for _, g := range gates.Registry {
		s.Gates[g.ID] = &GateState{
			Gate:   g,
			Status: StatusPending,
		}
	}

	return s
}
