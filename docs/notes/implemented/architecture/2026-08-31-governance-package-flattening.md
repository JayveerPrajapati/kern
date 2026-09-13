# Agent Note: Governance Package Flattening
Status: implemented

## Problem
internal/governance subpackages (approval/audit/exec/firewall/identity/risk) made cross-package
wiring and imports heavy.

## Decision
Flattened into root internal/governance/*.go; same commit added authorize-context, evidence bundles,
approval workflow, and store/types.

## Consequence
Old subpackage import paths are invalid; use the flattened root paths.
