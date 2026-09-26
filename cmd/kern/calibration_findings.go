package main

// calibrationFindings renders the calibration health section (prediction vs
// reality: recorded predictions, matched outcomes, per-subsystem confidence)
// as doctor findings, mirroring archDriftFindings. It is non-fatal: the
// section reports "ok" whenever the health text renders; only a failure to
// compute it (unreadable store, missing platform) is a warn.
//
// The check lives here (cmd/kern) rather than inside internal/doctor so the
// app/calibrate dependency stays off internal/doctor's documented allowed-deps
// set — cmd/kern is not a row in the ARCHITECTURE.md table, so no table
// change is required.

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/doctor"
)

func calibrationFindings(root string) []doctor.Finding {
	p, err := app.New(root)
	if err != nil {
		return []doctor.Finding{{
			Check:  "calibration",
			Level:  "warn",
			Detail: fmt.Sprintf("calibration health unavailable: %v", err),
		}}
	}
	ts := app.NewTaskService(p, nil)
	health, err := ts.CalibrationHealth()
	if err != nil {
		return []doctor.Finding{{
			Check:  "calibration",
			Level:  "warn",
			Detail: fmt.Sprintf("calibration health unavailable: %v", err),
		}}
	}
	return []doctor.Finding{{
		Check:  "calibration",
		Level:  "ok",
		Detail: strings.TrimSpace(health),
	}}
}
