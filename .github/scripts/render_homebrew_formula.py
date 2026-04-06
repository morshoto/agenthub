#!/usr/bin/env python3
from __future__ import annotations

import argparse
from pathlib import Path


FORMULA_TEMPLATE = """class Agenthub < Formula
  desc "CLI for provisioning and operating AgentHub environments"
  homepage "https://github.com/morshoto/agenthub"
  url "{url}"
  sha256 "{sha256}"
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
      -X agenthub/internal/app.Version=v#{{version}}
      -X agenthub/internal/app.CommitSHA=unknown
      -X agenthub/internal/app.BuildDate=unknown
    ].join(" ")

    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/agenthub"
  end

  test do
    output = shell_output("#{{bin}}/agenthub version")
    assert_match "agenthub v#{{version}}", output
    assert_match "commit: unknown", output
    assert_match "build date: unknown", output
  end
end
"""


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", required=True)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()

    rendered = FORMULA_TEMPLATE.format(
        url=args.url,
        sha256=args.sha256,
    )

    output_path = Path(args.output)
    output_path.write_text(rendered.rstrip() + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
