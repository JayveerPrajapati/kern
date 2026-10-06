# Fix the pagination off-by-one

`Items` in `list.go` returns one element too many: `Items(0, 2, xs)` returns
three elements, not two. The test file documents the exact expected
behavior. Fix the bug without duplicating logic the file already has.
