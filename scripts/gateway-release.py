#!/usr/bin/env python3
"""Validate the triggering gateway tag and emit GitHub Actions build outputs."""
import re
import sys


def main():
    if len(sys.argv) != 3:
        raise ValueError("usage: gateway-release.py gateway/vX.Y.Z IMAGE")
    tag, image = sys.argv[1:]
    number = r"(?:0|[1-9][0-9]*)"
    match = re.fullmatch(rf"gateway/v({number}\.{number}\.{number})(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?", tag)
    if not match:
        raise ValueError("gateway tag must be gateway/vX.Y.Z or gateway/vX.Y.Z-PRERELEASE (no build metadata)")
    preview = match.group(2)
    if preview and any(part.isdigit() and len(part) > 1 and part[0] == "0" for part in preview.split(".")):
        raise ValueError("numeric prerelease identifiers cannot have leading zeros")
    version = tag.removeprefix("gateway/v")
    if len(version) > 128:
        raise ValueError("version exceeds Docker's 128 character tag limit")
    print(f"version={version}")
    print(f"prerelease={str(bool(preview)).lower()}")
    print("tags<<SAEL_TAGS")
    print(f"{image}:{version}")
    if not preview:
        print(f"{image}:latest")
    print("SAEL_TAGS")


if __name__ == "__main__":
    try:
        main()
    except ValueError as error:
        print(error, file=sys.stderr)
        sys.exit(1)
