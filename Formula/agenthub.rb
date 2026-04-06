class Agenthub < Formula
  desc "CLI for provisioning and operating AgentHub environments"
  homepage "https://github.com/morshoto/agenthub"
  url "https://github.com/morshoto/agenthub/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "173fcee428bed572235202b51433e8d753ae91701514f897079dcf966c6958a8"
  license "MIT"

  livecheck do
    url :stable
    strategy :github_latest
  end

  depends_on "go" => :build

  def install
    ldflags = %W[
      -s
      -w
      -X agenthub/internal/app.Version=v#{version}
      -X agenthub/internal/app.CommitSHA=unknown
      -X agenthub/internal/app.BuildDate=unknown
    ].join(" ")

    cd buildpath do
      puts pwd
      puts "top-level entries:"
      puts Dir.children(".").sort
      unless File.exist?("go.mod")
        archive = Dir.glob("*.tar.gz*").first
        system "tar", "-xzf", archive, "--strip-components=1" if archive
      end
      puts "top-level entries after extract:"
      puts Dir.children(".").sort
      puts "go.mod files:"
      puts Dir.glob("**/go.mod")
      system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/agenthub"
    end
  end

  test do
    output = shell_output("#{bin}/agenthub version")
    assert_match "agenthub v#{version}", output
    assert_match "commit: unknown", output
    assert_match "build date: unknown", output
  end
end
