import os
import re
import sys


def main():
    tag = os.getenv("TAG")
    if tag is None:
        print("No tag provided")
        sys.exit(1)

    semver = re.compile(
        r"(?P<major>0|[1-9]\d*)\."
        r"(?P<minor>0|[1-9]\d*)\."
        r"(?P<patch>0|[1-9]\d*)"
        r"(?:-(?P<prerelease>(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?"
        r"(?:\+(?P<buildmetadata>[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$"
    )

    gh_output_path = os.getenv("GITHUB_OUTPUT")
    if gh_output_path is None:
        print("GITHUB_OUTPUT env var is empty")
        sys.exit(1)

    print(f"GITHUB_OUTPUT: {gh_output_path}")
    if not semver.match(tag):
        print(f"Invalid SemVer: {tag}")
        with open(gh_output_path, "a") as f:
            f.write("valid=false")
        return 0

    print(f"Valid SemVer: {tag}")
    with open(gh_output_path, "a") as f:
        f.write("valid=true")


if __name__ == "__main__":
    main()
