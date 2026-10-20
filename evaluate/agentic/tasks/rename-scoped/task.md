# Rename cache.New without touching queue.New

Both the `cache` and `queue` packages define a constructor named `New`.
Rename the constructor in the `cache` package to `Open`, and update every
caller of it (production code and tests). The `queue` package's `New` is
unrelated and must keep its name and its callers. The repository's tests
must still pass.
