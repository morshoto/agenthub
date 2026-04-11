#!/usr/bin/env python3

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

SEMVER_RE = re.compile(r"^v\d+\.\d+\.\d+$")
PREFIX_RE = re.compile(r"^(?P<type>[A-Za-z]+):\s*(?P<summary>.+)$")

SHORT_TYPE_TO_SECTION = {
    "add": "Features",
    "improve": "Features",
    "fix": "Fixes",
    "basicfix": "Fixes",
    "security": "Fixes",
    "performance": "Fixes",
    "docs": "Documentation",
    "dependency": "Maintenance",
}
SHORT_HIDDEN_TYPES = {
    "config",
    "conflicts",
    "design",
    "files",
    "idea",
    "log",
    "move",
    "refactor",
    "review",
    "style",
    "test",
    "tracking",
    "update",
    "wip",
}
FULL_TYPE_TO_SECTION = {
    "add": "Features",
    "improve": "Features",
    "fix": "Fixes",
    "basicfix": "Fixes",
    "security": "Fixes",
    "performance": "Fixes",
    "docs": "Documentation",
    "config": "Maintenance",
    "dependency": "Maintenance",
    "update": "Maintenance",
    "conflicts": "Internal",
    "design": "Internal",
    "files": "Internal",
    "idea": "Internal",
    "log": "Internal",
    "move": "Internal",
    "refactor": "Internal",
    "review": "Internal",
    "style": "Internal",
    "test": "Internal",
    "tracking": "Internal",
    "wip": "Internal",
}
SHORT_SECTION_ORDER = ["Features", "Fixes", "Documentation", "Maintenance", "Other"]
FULL_SECTION_ORDER = ["Features", "Fixes", "Documentation", "Maintenance", "Internal", "Other"]


@dataclass(frozen=True)
class Commit:
    summary: str
    short_sha: str
    commit_type: str | None


@dataclass(frozen=True)
class ReleaseSlice:
    version: str
    date: str
    commits: list[Commit]


def run_git(args: list[str]) -> str:
    result = subprocess.run(
        ["git", *args],
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout.strip()


def parse_commit_subject(subject: str) -> tuple[str | None, str]:
    subject = subject.strip()
    match = PREFIX_RE.match(subject)
    if not match:
        return infer_commit_type(None, subject), subject
    commit_type = match.group("type").lower()
    summary = match.group("summary").strip()
    return infer_commit_type(commit_type, summary), summary or subject


def infer_commit_type(commit_type: str | None, summary: str) -> str | None:
    summary_lower = summary.lower()
    if any(token in summary_lower for token in ("readme", "contribution", "contributing", "changelog", "docs")):
        return "docs"

    if commit_type:
        return commit_type

    first_word = re.split(r"[\s:/_-]+", summary_lower, maxsplit=1)[0]
    aliases = {
        "add": "add",
        "disable": "update",
        "delete": "files",
        "fix": "fix",
        "narrow": "update",
        "preserve": "update",
        "revert": "update",
        "revise": "docs",
        "update": "update",
    }
    return aliases.get(first_word)


def load_commits(rev_range: str) -> list[Commit]:
    raw = run_git(["log", "--no-merges", "--pretty=format:%s%x1f%h", rev_range])
    if not raw:
        return []

    commits: list[Commit] = []
    for line in raw.splitlines():
        subject, short_sha = line.split("\x1f", 1)
        commit_type, summary = parse_commit_subject(subject)
        commits.append(Commit(summary=summary, short_sha=short_sha, commit_type=commit_type))
    return commits


def load_release_slices() -> list[ReleaseSlice]:
    raw_tags = run_git(["for-each-ref", "--sort=-v:refname", "--format=%(refname:short)|%(creatordate:short)", "refs/tags"])
    tags: list[tuple[str, str]] = []
    for line in raw_tags.splitlines():
        tag, date = line.split("|", 1)
        if SEMVER_RE.match(tag):
            tags.append((tag, date))

    slices: list[ReleaseSlice] = []
    for index, (tag, date) in enumerate(tags):
        rev_range = tag if index == len(tags) - 1 else f"{tags[index + 1][0]}..{tag}"
        slices.append(ReleaseSlice(version=tag, date=date, commits=load_commits(rev_range)))

    if tags:
        unreleased_commits = load_commits(f"{tags[0][0]}..HEAD")
        if unreleased_commits:
            head_date = run_git(["log", "-1", "--format=%cs", "HEAD"])
            slices.insert(0, ReleaseSlice(version="Unreleased", date=head_date, commits=unreleased_commits))

    return slices


def group_commits(commits: list[Commit], type_to_section: dict[str, str], hidden_types: set[str] | None = None) -> dict[str, list[str]]:
    sections = {name: [] for name in FULL_SECTION_ORDER}
    hidden = hidden_types or set()

    for commit in commits:
        if commit.commit_type in hidden:
            continue
        if commit.commit_type is None:
            section = "Other"
        else:
            section = type_to_section.get(commit.commit_type, "Other")
        sections.setdefault(section, []).append(f"{commit.summary} ({commit.short_sha})")

    return sections


def render_release_notes(version: str, repo_url: str, commits: list[Commit], checksums_path: Path) -> str:
    visible_sections = group_commits(commits, SHORT_TYPE_TO_SECTION, SHORT_HIDDEN_TYPES)
    if not any(visible_sections.get(name) for name in SHORT_SECTION_ORDER):
        visible_sections = group_commits(commits, FULL_TYPE_TO_SECTION)
        if not any(visible_sections.get(name) for name in FULL_SECTION_ORDER):
            visible_sections["Maintenance"] = ["No changes recorded."]

    lines = [f"# {version}", ""]
    lines.append("Full release history: " + f"{repo_url}/blob/main/doc/changelog.md")
    lines.append("")

    for section in SHORT_SECTION_ORDER:
        entries = visible_sections.get(section, [])
        if not entries:
            continue
        lines.append(f"## {section}")
        lines.extend(f"- {entry}" for entry in entries)
        lines.append("")

    checksums = checksums_path.read_text().strip()
    lines.append("## Checksums")
    if checksums:
        lines.append("```text")
        lines.extend(checksums.splitlines())
        lines.append("```")
    else:
        lines.append("No checksums generated.")
    lines.append("")
    return "\n".join(lines)


def render_full_changelog(repo_url: str, slices: list[ReleaseSlice]) -> str:
    lines = ["# Changelog", "", "Versioned release history for `agenthub`.", ""]
    lines.append(f"GitHub Releases: {repo_url}/releases")
    lines.append("")

    for release in slices:
        lines.append(f"## {release.version} - {release.date}")
        if not release.commits:
            lines.append("")
            lines.append("No changes recorded.")
            lines.append("")
            continue

        sections = group_commits(release.commits, FULL_TYPE_TO_SECTION)
        for section in FULL_SECTION_ORDER:
            entries = sections.get(section, [])
            if not entries:
                continue
            lines.append("")
            lines.append(f"### {section}")
            lines.extend(f"- {entry}" for entry in entries)
        lines.append("")

    return "\n".join(lines)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Render release notes and changelog content from git history.")
    parser.add_argument("--version", help="Release version for the generated release notes.")
    parser.add_argument("--previous-tag", default="", help="Previous semver tag used to compute the release range.")
    parser.add_argument("--target-ref", default="HEAD", help="Target git ref for release note generation.")
    parser.add_argument("--repo-url", required=True, help="Repository URL used for changelog links.")
    parser.add_argument("--checksums", type=Path, help="Path to checksums.txt for embedding in release notes.")
    parser.add_argument("--release-notes-out", type=Path, help="Output path for generated release notes.")
    parser.add_argument("--full-changelog-out", type=Path, help="Output path for the generated full changelog.")
    return parser


def main() -> int:
    args = build_parser().parse_args()

    if not args.release_notes_out and not args.full_changelog_out:
        print("at least one output path is required", file=sys.stderr)
        return 1

    if args.release_notes_out:
        if not args.version:
            print("--version is required when generating release notes", file=sys.stderr)
            return 1
        if not args.checksums:
            print("--checksums is required when generating release notes", file=sys.stderr)
            return 1
        rev_range = f"{args.previous_tag}..{args.target_ref}" if args.previous_tag else args.target_ref
        release_notes = render_release_notes(args.version, args.repo_url, load_commits(rev_range), args.checksums)
        args.release_notes_out.write_text(release_notes)

    if args.full_changelog_out:
        args.full_changelog_out.write_text(render_full_changelog(args.repo_url, load_release_slices()))

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
