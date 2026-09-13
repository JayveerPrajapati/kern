# Agent Note: Generated Files Hidden From Every Lens
Status: implemented

## Problem
kern's own generated files (.kern/, .opencode/, agent configs) polluted its own index, search, pack,
and git views.

## Decision
Self-hide across four lenses: .gitignore kern-generated block; index ignoreDirs blocks
.kern/.opencode/.claude/...; ignore.go vcsDirs hardcodes .kern; pack honors .gitignore+.kernignore.
Raw filesystem tools still see them (they are kern's data store).

## Consequence
kern's analysis lenses never surface kern's own artifacts; this is by design, not a leak.
