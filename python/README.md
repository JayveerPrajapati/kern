# kern PyPI shim

`pip install kern-context` gives you the `kern` command. This is a thin
wrapper: on first invocation it downloads the prebuilt Go binary from the
project's GitHub Releases, caches it in `~/.cache/kern-pip`, and execs it.

## Environment overrides

| Variable | Default | Purpose |
|---|---|---|
| `KERN_VERSION` | latest | Pin a release (e.g. `1.2.3`) |
| `KERN_REPO` | `JayveerPrajapati/kern` | Override the release repo |

## Alternative installs

Prefer one of these if you don't use pip:

```sh
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh | sh

# Homebrew (once the tap is published)
brew install kern

# From source (requires Go 1.23+)
go install github.com/JayveerPrajapati/kern/cmd/kern@latest
go install github.com/JayveerPrajapati/kern/cmd/kern-mcp@latest
```
