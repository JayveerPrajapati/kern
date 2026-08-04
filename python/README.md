# kern PyPI shim
#
# `pip install kern-context` gives you the `kern` command. This is a thin
# wrapper: on first invocation it downloads the prebuilt Go binary from the
# project's GitHub Releases, caches it in ~/.cache/kern-pip, and execs it.
#
# Environment overrides:
#   KERN_VERSION=1.2.3   pin a release (default: latest)
#   KERN_REPO=you/kern   override the release repo (default: JayveerPrajapati/kern)
#
# For installs without pip, prefer:
#   curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh | sh
# or Homebrew, or `go install github.com/JayveerPrajapati/kern/cmd/kern@latest`.
