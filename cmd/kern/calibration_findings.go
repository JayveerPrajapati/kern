package main

// calibrationFindings renders the calibration health section (prediction vs
// reality: recorded predictions, matched outcomes, per-subsystem confidence)
// as doctor findings, mirroring archDriftFindings. It is non-fatal: the
// section reports "ok" whenever the health text renders; only a failure to
// compute it (unreadable store, missing platform) is a warn.

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/doctor"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
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
	ts := tasklife.NewTaskService(p, nil)
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
