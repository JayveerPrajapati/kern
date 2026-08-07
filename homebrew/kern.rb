# Homebrew formula for kern.
#
# Two ways to use this:
#
# 1. Your own tap (recommended):
#      brew tap JayveerPrajapati/tap  https://github.com/JayveerPrajapati/homebrew-tap
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
# Distribution note: run scripts/brew-release.sh after each release — it
# prints this formula with the correct SHA256 sums filled in.

class Kern < Formula
  desc "Local-only context optimizer for AI agents (compression, indexing, agent wiring)"
  homepage "https://github.com/JayveerPrajapati/kern"
  url "https://github.com/JayveerPrajapati/kern/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "REPLACE_WITH_SOURCE_TARBALL_SHA256"
  license "MIT"

  depends_on "go" => :build

  def install
    system "go", "build", "-buildvcs=false", "-ldflags", "-X main.version=#{version}", "-o", "kern", "./cmd/kern"
    system "go", "build", "-buildvcs=false", "-ldflags", "-X main.version=#{version}", "-o", "kern-mcp", "./cmd/kern-mcp"
    # macOS Gatekeeper kills unsigned binaries with SIGKILL (exit 137); apply
    # an ad-hoc signature so the built copies are executable.
    if OS.mac?
      system "codesign", "--force", "--sign", "-", "kern"
      system "codesign", "--force", "--sign", "-", "kern-mcp"
    end
    bin.install "kern"
    bin.install "kern-mcp"
  end

  test do
    assert_match /kern v?\d+\./, shell_output("#{bin}/kern version")
    assert_predicate bin/"kern-mcp", :exist?
  end
end
