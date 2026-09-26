// Calibration wiring — Feature Batch C (prediction vs reality).
//
// The app layer is the caller side of the calibration contract:
//   - it records impact/what-if predictions at the choke points
//     (internal/calibrate.RecordPrediction, best-effort);
//   - it gathers observed outcomes from the incident store and matches them
//     against the recorded predictions (calibrate.MatchOutcomes);
//   - it exposes the health summary and the verify confidence line to the
//     CLI/MCP surfaces and the doctor section.
package app

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/calibrate"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// recordPrediction persists one calibration prediction for root. Recording
// is best-effort by design: it never fails or changes the caller's path — a
// failed append is logged and ignored.
func (s *TaskService) recordPrediction(root string, p calibrate.Prediction) {
	if err := calibrate.RecordPrediction(root, p); err != nil {
		log.Printf("kern app: calibration prediction NOT recorded (%s): %v", p.Kind, err)
	}
}

// recordWhatIfPrediction records the what-if simulation as an "impact"-kind
// prediction (what-if is impact's alias, so its kinds fold in). The predicted
// file set is the simulation's Files field; the subsystem is derived from the
// resolved target's defining file.
func (s *TaskService) recordWhatIfPrediction(change string, imp whatif.Impact) {
	target := imp.Change.Target
	s.recordPrediction(s.platform.Root(), calibrate.Prediction{
		Kind:           "impact",
		Change:         change,
		Target:         target,
		Subsystem:      s.subsystemOfTarget(target),
		PredictedFiles: imp.Files,
		TS:             time.Now(),
	})
}

// recordImpactPrediction records the impact analysis as an "impact"-kind
// prediction. The predicted file set is the file set the impact path computed
// (the defining files of the target and every symbol the report names);
// the subsystem is derived from the target's defining file.
func (s *TaskService) recordImpactPrediction(change string, rep *domain.ImpactReport, predFiles []string) {
	s.recordPrediction(s.platform.Root(), calibrate.Prediction{
		Kind:           "impact",
		Change:         change,
		Target:         rep.Target,
		Subsystem:      s.subsystemOfTarget(rep.Target),
		PredictedFiles: predFiles,
		TS:             time.Now(),
	})
}

// subsystemOfTarget derives the calibration subsystem of a resolved symbol
// from its defining file ("" when the file cannot be resolved).
func (s *TaskService) subsystemOfTarget(target string) string {
	if f := s.platform.graphNodeFile(target); f != "" {
		return calibrate.SubsystemOf(f)
	}
	return ""
}

// VerifyConfidenceLine returns the aggregate calibration confidence line for
// kern verify text output (best-effort: "" when the model has no data or a
// read fails). The CLI and MCP render paths call this so both surfaces get
// the line without importing internal/calibrate themselves.
func VerifyConfidenceLine(root string) string {
	return calibrate.AggregateConfidenceLine(root)
}

// CalibrationHealth returns the calibration health summary for the
// platform's root: it gathers observed outcomes from the incident store,
// matches them against the recorded predictions, and renders the
// per-subsystem model. When the incident store is empty the summary reports
// "no outcome data" gracefully (a fresh store is not an error).
func (s *TaskService) CalibrationHealth() (string, error) {
	if s.platform == nil {
		return "", fmt.Errorf("task service: platform not configured")
	}
	root := s.platform.Root()
	incs, err := incident.NewStore(root).List()
	if err != nil {
		return "", fmt.Errorf("calibration: incident store: %w", err)
	}
	if len(incs) > 0 {
		// Model BEFORE this match pass (pre-existing outcomes): the baseline
		// for each new outcome's "confidence before" in the typed-claim
		// memories.
		before, _ := calibrate.Model(root)
		matched, err := calibrate.MatchOutcomes(root, incidentOutcomeSources(incs))
		if err != nil {
			log.Printf("kern app: calibration outcome match failed: %v", err)
		} else {
			s.recordCalibrationClaims(matched, before)
		}
	}
	health, err := calibrate.Health(root)
	if err != nil {
		return "", err
	}
	if len(incs) == 0 {
		health += "\nno outcome data (incident store empty) — predictions accumulate until an incident is observed"
	}
	return health, nil
}

// incidentOutcomeSources maps stored incidents to caller-supplied outcome
// sources. Each incident contributes one source: the affected files from its
// root cause (sorted for determinism), a subsystem derived from the first
// file, kind "impact" (an incident is the observed outcome of a change's
// impact), and the incident's creation time.
func incidentOutcomeSources(incs []domain.Incident) []calibrate.OutcomeSource {
	out := make([]calibrate.OutcomeSource, 0, len(incs))
	for _, inc := range incs {
		var files []string
		if inc.RootCause != nil {
			files = append([]string(nil), inc.RootCause.Files...)
		}
		sort.Strings(files)
		sub := ""
		if len(files) > 0 {
			sub = calibrate.SubsystemOf(files[0])
		}
		out = append(out, calibrate.OutcomeSource{
			ID:        inc.ID,
			Kind:      "impact",
			Subsystem: sub,
			Files:     files,
			TS:        inc.CreatedAt,
		})
	}
	return out
}

// recordCalibrationClaims writes a typed-claim memory for every newly matched
// outcome through the learning path — the "WhatIf said X, reality said Y"
// calibration signal written into engineering memory. Each memory is an
// INFERENCE claim (derived from the prediction record + the observed
// outcome) with a deterministically assembled statement — no LLM — and
// provenance = prediction log entry id/timestamp + outcome source. It is
// opt-in: it only runs when the platform is wired with the calibration store
// and a memory store (mirroring the nil-guards around prediction recording);
// an absent store writes nothing and never panics. learning.Remember() upserts
// by scope, so repeated observations of the same subsystem refresh one
// constraint instead of self-reinforcing into duplicates.
//
// before is the per-subsystem model as of the START of the match pass
// (pre-existing outcomes). matched is ordered deterministically (outcome
// timestamp, then source ID), so the incremental hit/miss fold below yields a
// stable before/after confidence per outcome.
func (s *TaskService) recordCalibrationClaims(matched []calibrate.Outcome, before []calibrate.SubsystemConfidence) {
	if s.platform == nil || s.platform.Memory() == nil || len(matched) == 0 {
		return
	}
	counts := map[string][2]int{} // subsystem -> [hits, misses] before this pass
	for _, c := range before {
		counts[c.Subsystem] = [2]int{c.Hits, c.Misses}
	}
	ex := learning.New(s.platform.Memory())
	for _, o := range matched {
		c := counts[o.Subsystem]
		beforeConf := confidenceOf(c[0], c[1])
		if o.Hit {
			c[0]++
		} else {
			c[1]++
		}
		counts[o.Subsystem] = c
		afterConf := confidenceOf(c[0], c[1])
		p := calibrationClaimPattern(o, beforeConf, afterConf, c[0]+c[1])
		if _, err := ex.Remember(p); err != nil {
			log.Printf("kern app: calibration claim memory NOT recorded: %v", err)
		}
	}
}

// confidenceOf returns the confidence of a hits/misses pair (0 when there are
// no samples).
func confidenceOf(hits, misses int) float64 {
	if hits+misses == 0 {
		return 0
	}
	return float64(hits) / float64(hits+misses)
}

// calibrationClaimPattern assembles the typed-claim Pattern for one matched
// outcome. The statement is built purely from structured fields: the
// subsystem, the predicted target, the verdict (WRONG when the outcome
// contradicts the prediction, CONFIRMED when it matches), the cumulative case
// count, and the confidence before/after the outcome. The scope is stable per
// subsystem so Remember() upserts one constraint per subsystem; the written
// memory carries ClaimType INFERENCE, so the extractor groups it under
// "claim:INFERENCE:scope:<scope>" — exactly the key its pattern surfaces
// under.
func calibrationClaimPattern(o calibrate.Outcome, before, after float64, n int) learning.Pattern {
	verdict := "CONFIRMED"
	if !o.Hit {
		verdict = "WRONG"
	}
	target := o.Target
	if target == "" {
		target = o.Change
	}
	if target == "" {
		target = "unknown"
	}
	statement := fmt.Sprintf(
		"subsystem %s prediction (impact of %s) was %s in %d cases (confidence before %.1f%%, after %.1f%%)",
		o.Subsystem, target, verdict, n, before*100, after*100)
	scope := "calibration:impact:" + o.Subsystem
	srcs := []string{
		"prediction " + o.PredID + " @ " + o.PredTS.UTC().Format(time.RFC3339),
		"outcome " + o.SrcID + " @ " + o.SrcTS.UTC().Format(time.RFC3339),
	}
	sort.Strings(srcs)
	return learning.Pattern{
		Key:       scope,
		Count:     n,
		Scopes:    []string{o.Subsystem},
		Sample:    []string{statement},
		Created:   o.SrcTS,
		ClaimType: domain.ClaimInference,
		Provenance: learning.ClaimProvenance{
			Sources: srcs,
			Count:   1,
			Latest:  o.SrcTS,
		},
		Statement: statement,
	}
}
