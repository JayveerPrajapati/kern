# Homebrew formula for kern.
#
# This is a GENERIC template: the version and sha256 are filled in at release
# time from the git tag, so the committed copy always points at whatever tag it
# was last released under. You normally never edit this file by hand.
#
# Three ways to use this:
#
# 1. Your own tap (recommended):
#      brew tap <owner>/tap  https://github.com/<owner>/homebrew-tap
#      brew install kern
#    Publish the tap with scripts/publish-tap.sh (creates the repo, computes
#    the sha256 from the GitHub source tarball, pushes Formula/kern.rb).
#
#    The release workflow also attaches a ready-to-use `kern.rb` (url + sha256
#    filled in for the tag) to every GitHub Release — download it from the
#    release's assets instead of editing by hand.
#
# 2. Local brew install from source (no release needed):
#      brew install --build-from-source ./homebrew/kern.rb
#
# 3. Latest dev build from the default branch (no release needed):
#      brew install --HEAD ./homebrew/kern.rb
#
# The version/sha256 are substituted by scripts/brew-release.sh and
# scripts/publish-tap.sh (and by the release workflow) using the placeholders
# below. Set the env vars KERN_REPO / KERN_REPO_OWNER to retarget.

class Kern < Formula
  desc "Local-only context optimizer for AI agents (compression, indexing, agent wiring)"
  homepage "https://github.com/JayveerPrajapati/kern"

  # Placeholders substituted at release time by the tooling; keep these exact
  # (scripts/brew-release.sh and scripts/publish-tap.sh sed on them). The URL
  # carries the version placeholder directly (NOT #{version}) because Homebrew
  # interpolates #{version} to an empty string in a url string on some versions,
  # which would break every release download.
  url "https://github.com/JayveerPrajapati/kern/archive/refs/tags/__KERN_VERSION__.tar.gz"
  version "__KERN_VERSION__"
  sha256 "__KERN_SHA256__"
  license "MIT"

  depends_on "go" => :build

  def install
    # Pin the Go toolchain to a known-good release that satisfies go.mod's
    # `go 1.23` directive; Homebrew's go shim honors HOMEBREW_GO_VERSION.
    ENV["HOMEBREW_GO_VERSION"] = "1.23"
    system "go", "build", "-buildvcs=false", "-tags", "sqlite", "-ldflags", "-X main.version=#{version}", "-o", "kern", "./cmd/kern"
    system "go", "build", "-buildvcs=false", "-tags", "sqlite", "-ldflags", "-X main.version=#{version}", "-o", "kern-mcp", "./cmd/kern-mcp"
    system "go", "build", "-buildvcs=false", "-tags", "sqlite", "-ldflags", "-X main.version=#{version}", "-o", "kern-server", "./cmd/kern-server"
    # macOS Gatekeeper kills unsigned binaries with SIGKILL (exit 137); apply
    # an ad-hoc signature so the built copies are executable.
    if OS.mac?
      system "codesign", "--force", "--sign", "-", "kern"
      system "codesign", "--force", "--sign", "-", "kern-mcp"
      system "codesign", "--force", "--sign", "-", "kern-server"
    end
    bin.install "kern"
    bin.install "kern-mcp"
    bin.install "kern-server"
  end

  test do
    assert_match /kern v?\d+\./, shell_output("#{bin}/kern version")
    assert_predicate bin/"kern-mcp", :exist?
    assert_predicate bin/"kern-server", :exist?
  end

  def caveats
    <<~EOS
      kern is installed! To wire it into your AI agents:

        kern setup --global

      This writes the kern-first policy to ~/AGENTS.md, ~/.claude/CLAUDE.md,
      and installs the opencode plugin globally — so agents in ANY project
      use kern tools (not just the project where you run setup).

      To wire a specific project, run from its root:
        kern setup --detect

      Restart your agent (opencode reload / claude restart) after wiring.
    EOS
  end
end