"""Review Git's index for prohibited publication artifacts, without printing secrets."""
from pathlib import PurePosixPath
import re
import subprocess
import sys

ALLOWED_SUFFIXES = {".go", ".mod", ".sum", ".md", ".json", ".yml", ".yaml", ".py"}
ALLOWED_NAMES = {".gitignore", "LICENSE", "example.env"}
PRIVATE_DIRECTORIES = {"artifacts", "work", "captures", "profiles", "private-nso", "private-gpu"}
PATTERNS = (
    ("private signing material", re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----")),
    ("embedded bearer credential", re.compile(r"Bearer\s+[A-Za-z0-9_./+=-]{40,}")),
    ("embedded GitHub credential", re.compile(r"(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})")),
    ("embedded AWS access key", re.compile(r"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b")),
    ("embedded JWT", re.compile(r"\beyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b")),
    ("private local user path", re.compile(r"(?i)\bC:[/\\]+Users[/\\]+")),
)


def git(*args):
    return subprocess.check_output(["git", *args])


def main():
    paths = [p.decode("utf-8") for p in git("ls-files", "-z").split(b"\0") if p]
    failures = []
    for name in paths:
        path = PurePosixPath(name)
        if any(part.lower() in PRIVATE_DIRECTORIES for part in path.parts):
            failures.append((name, "private runtime directory"))
        if path.suffix.lower() not in ALLOWED_SUFFIXES and path.name not in ALLOWED_NAMES:
            failures.append((name, "file type outside the publication allowlist"))
        if name.lower().endswith(".local.json") or path.name == ".env":
            failures.append((name, "local configuration"))
        data = git("show", ":" + name)
        if len(data) > 2_000_000:
            failures.append((name, "oversized source/documentation file requires review"))
        try:
            text = data.decode("utf-8")
        except UnicodeDecodeError:
            failures.append((name, "non-UTF-8 content"))
            continue
        if "\0" in text:
            failures.append((name, "binary content"))
        for label, pattern in PATTERNS:
            if pattern.search(text):
                failures.append((name, label))
    for name, label in failures:
        print(f"FAIL: {name}: {label}", file=sys.stderr)
    if failures:
        return 1
    print(f"Publication content check passed for {len(paths)} indexed files. Human provenance review is still required.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
