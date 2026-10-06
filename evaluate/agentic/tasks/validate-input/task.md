# Validate Save input

The `Save` method in `store/store.go` accepts anything, including empty
names and zero or negative quantities. Make it reject those inputs, reusing
what this repository already provides instead of writing new validation
logic. The repository's tests describe the exact expected behavior.
