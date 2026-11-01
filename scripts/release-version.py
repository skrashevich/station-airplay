#!/usr/bin/env python3
"""Map Git tags (including v0.1) to YAM's required major.minor.patch SemVer."""
import re
import sys


def normalize(tag):
    match = re.fullmatch(r"v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:\.(0|[1-9][0-9]*))?", tag)
    if not match:
        raise ValueError("expected a release version such as v0.1 or v0.1.0")
    return f"{match[1]}.{match[2]}.{match[3] or '0'}"


if __name__ == "__main__":
    try:
        print(normalize(sys.argv[1]))
    except (IndexError, ValueError) as error:
        sys.exit(str(error))
