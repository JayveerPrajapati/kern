// This file holds canonical domain entities. Like the rest of this package,
// these are pure types: storage-agnostic, provider-independent, and free of
// MCP, CLI, REST, or SDK specifics.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Team is a group of owners (spec §7 node type, F-21). Teams own
// files/symbols (via ownership.Map) and are graph nodes.
type Team struct {
	ID             string   // stable identifier (e.g. "@backend-team")
	Name           string   // display name
	Members        []string // member handles/emails
	OwnershipScope string   // scope prefix for owned paths (e.g. "src/backend/")
}

// Service is a deployable unit (microservice, package group, binary).
type Service struct {
	ID           string // stable identifier
	Name         string
	Path         string   // containing directory/package
	Type         string   // "api", "worker", "package", "cli", etc.
	Dependencies []string // service IDs it depends on
}

// API is an externally reachable endpoint (HTTP/CLI/RPC).
type API struct {
	ID        string
	Name      string
	Method    string // "GET", "POST", ... ("" when N/A)
	Path      string // route pattern
	Service   string // owning Service ID
	Symbol    string // handler symbol qualified name
	Framework string // framework the API was registered with, e.g. "gin"
	File      string // source file (relative path) where the route is registered
	Line      int    // line number of the route registration
}

// Database is a data store the system depends on.
type Database struct {
	ID     string
	Name   string
	Type   string // "postgres", "mysql", "sqlite", ...
	Kind   string // "relational", "document", "kv", ...
	Tables []string
}

// Table is a table/collection in a Database.
type Table struct {
	ID       string
	Name     string
	Database string
	Columns  []string
}

// Topic is a message/event topic.
type Topic struct {
	ID        string
	Name      string
	Type      string // "topic", "queue", "stream", ...
	Broker    string // message broker: "kafka", "rabbitmq", "redis", "nats"
	Producers []string
	Consumers []string
}

// Commit is a version-control commit.
type Commit struct {
	ID      string // full hash
	Short   string // short hash
	Message string
	Author  string
	Files   []string
	When    time.Time
}

// PullRequest is a code-review / merge request.
type PullRequest struct {
	ID       string
	Title    string
	Branch   string
	Base     string
	Commits  []string
	Status   string // "open", "merged", "closed"
	OpenedAt time.Time
}

// Permission grants an agent the right to perform an action on a resource.
type Permission struct {
	ID       string
	Agent    string // agent ID
	Resource string // resource identifier or "*"
	Action   string // "read", "write", "execute", "approve", ...
	Allowed  bool
}

// ArtifactKind is the canonical type of a workflow artifact (spec §54).
type ArtifactKind string

const (
	ArtifactContextPacket      ArtifactKind = "context_packet"
	ArtifactAnalysisReport     ArtifactKind = "analysis_report"
	ArtifactImpactReport       ArtifactKind = "impact_report"
	ArtifactRiskReport         ArtifactKind = "risk_report"
	ArtifactPlan               ArtifactKind = "plan"
	ArtifactCodePatch          ArtifactKind = "code_patch"
	ArtifactDiff               ArtifactKind = "diff"
	ArtifactTestReport         ArtifactKind = "test_report"
	ArtifactSecurityReport     ArtifactKind = "security_report"
	ArtifactArchitectureReport ArtifactKind = "architecture_report"
	ArtifactVerificationReport ArtifactKind = "verification_report"
	ArtifactIncidentReport     ArtifactKind = "incident_report"
	ArtifactRootCauseReport    ArtifactKind = "root_cause_report"
	ArtifactEvidenceReport     ArtifactKind = "evidence_report"
	ArtifactPullRequest        ArtifactKind = "pull_request"
	ArtifactDeployment         ArtifactKind = "deployment"
	ArtifactDeploymentReport   ArtifactKind = "deployment_report"
	ArtifactRollbackReport     ArtifactKind = "rollback_report"
	ArtifactMemoryEntry        ArtifactKind = "memory_entry"
	ArtifactAudit              ArtifactKind = "audit"
)

// Artifact is a named, traced output of an engineering workflow stage.
type Artifact struct {
	ID               string
	Kind             ArtifactKind // canonical artifact kind
	Type             string       // "context_packet", "plan", "code_patch", "diff", "report", ...
	TaskID           string       // originating Task ID
	CreatedBy        string       // agent ID that produced this artifact
	CreatedAt        time.Time
	Version          int      // artifact version (0 = initial)
	Status           string   // "draft", "final", "superseded"
	Scope            string   // what the artifact applies to
	Provenance       string   // how the artifact was produced
	URI              string   // location of the artifact
	Digest           string   // content hash
	ParentArtifactID string   // parent artifact in the traceable chain
	RelatedEntities  []string // related entity IDs
	Links            []ArtifactLink // typed links to other artifacts (P3.4)
}

// NewArtifact builds an Artifact with a stable, unique id and a deterministic
// content digest (SHA-256 over kind+task+uri). The ID embeds a short hash of
// the URI so distinct URIs for the same kind+task never collide. It is the
// canonical way to record a typed artifact in the workflow chain.
func NewArtifact(kind ArtifactKind, taskID, uri string) Artifact {
	base := string(kind) + "|" + taskID + "|" + uri
	digestSum := sha256.Sum256([]byte(base))
	digest := hex.EncodeToString(digestSum[:])
	// The ID is kind+task plus a short hash of the URI so it is unique per
	// kind+task+uri. shortHash is the first 8 hex chars of SHA-256 over the URI.
	uriSum := sha256.Sum256([]byte(uri))
	shortHash := hex.EncodeToString(uriSum[:])[:8]
	id := string(kind) + "-" + taskID + "-" + shortHash
	if taskID == "" {
		id = string(kind) + "-" + shortHash
	}
	return Artifact{
		ID:        id,
		Kind:      kind,
		Type:      string(kind),
		TaskID:    taskID,
		URI:       uri,
		Digest:    digest,
		CreatedAt: time.Now(),
	}
}

// ArtifactLinkKind is the semantic relationship between two artifacts
// (spec §54, Phase 3 P3.4). A link is directional: From is the dependent
// artifact, To is the artifact it relates to.
type ArtifactLinkKind string

const (
	// LinkDerivedFrom marks To as the source From was produced from (e.g. a
	// plan derived_from a risk_report).
	ArtifactLinkDerivedFrom ArtifactLinkKind = "derived_from"
	// ArtifactLinkSupports marks To as corroborating From (e.g. a test_report
	// supports a verification_report).
	ArtifactLinkSupports ArtifactLinkKind = "supports"
	// ArtifactLinkContradicts marks To as conflicting with From (e.g. a
	// security_report contradicts an architecture_report).
	ArtifactLinkContradicts ArtifactLinkKind = "contradicts"
)

// ArtifactLink is a typed, directional relationship between two artifacts.
// FromID is the dependent artifact; ToID is the referenced source. Linking
// makes the artifact traceability chain auditable: from a report you can walk
// both the artifacts it derives from and the artifacts that support or
// contradict it.
type ArtifactLink struct {
	FromID string           // source artifact ID
	ToID   string           // target artifact ID
	Kind   ArtifactLinkKind // derived_from | supports | contradicts
	Reason string           // optional human/agent rationale for the link
}

// NewArtifactLink builds a validated ArtifactLink. It refuses self-links and
// unknown kinds, returning an error so callers can't record an invalid edge in
// the traceable chain.
func NewArtifactLink(fromID, toID string, kind ArtifactLinkKind, reason string) (ArtifactLink, error) {
	if fromID == "" || toID == "" {
		return ArtifactLink{}, fmt.Errorf("artifact link: from and to ids are required")
	}
	if fromID == toID {
		return ArtifactLink{}, fmt.Errorf("artifact link: cannot link artifact %q to itself", fromID)
	}
	switch kind {
	case ArtifactLinkDerivedFrom, ArtifactLinkSupports, ArtifactLinkContradicts:
	default:
		return ArtifactLink{}, fmt.Errorf("artifact link: unknown kind %q", kind)
	}
	return ArtifactLink{FromID: fromID, ToID: toID, Kind: kind, Reason: reason}, nil
}

// VerificationResult is the outcome of a verification check.
type VerificationResult struct {
	ID      string
	Verdict string // "PASS", "FAIL", "WARN"
	Summary string
	Checks  []VerificationCheck
	RunAt   time.Time
}

// VerificationCheck is a single verification sub-check.
type VerificationCheck struct {
	Name   string // "build", "test", "security", "architecture", "dependency"
	Status string // "pass", "fail", "warn"
	Detail string
}
