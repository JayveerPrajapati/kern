# Agent Note: Plugin Syncs Across Four Locations
Status: implemented

## Problem
The opencode plugin existed in 4 copies (repo, embedded asset, both globals) that drifted; a stale
global silently won.

## Decision
setup.GlobalPluginPaths returns both global paths; wireGlobalPlugin installs with compare-and-write
(identical untouched, customized reported); parity test enforces repo==asset.

## Consequence
After editing the plugin: run kern setup + restart opencode; verify sha256 across all four.
