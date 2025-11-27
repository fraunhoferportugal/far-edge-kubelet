#!/usr/bin/env bash
set -euo pipefail

VERSION_FILE="VERSION"
PREFIX=""
WRITE=1
RELEASE=0
PRERELEASE=0

function usage() {
    echo "Usage:"
    echo "  $0 [--prefix=v] show"
    echo "  $0 [--prefix=v] [--dry-run] set <version>"
    echo "  $0 [--prefix=v] [--dry-run] bump <major|minor|patch|prerelease>"
    exit 1
}

function parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --prefix=*)
                PREFIX="${1#*=}"
                shift
                ;;
            --write)
                WRITE=1
                shift
                ;;
            --release)
                RELEASE=1
                shift
                ;;
            --prerelease)
                PRERELEASE=1
                shift
                ;;
            show|set|bump)
                CMD="$1"
                shift
                CMD_ARGS=("$@")
                break
                ;;
            *)
                echo "Unknown option: $1"
                usage
                ;;
        esac
    done
}

function read_version() {
    if [[ ! -f "$VERSION_FILE" ]]; then
        echo "${PREFIX}0.0.0" > "$VERSION_FILE"
    fi
    cat "$VERSION_FILE" | tr -d ' \t\n\r'
}

function write_version() {
    local v="$1"
    echo "$v" > "$VERSION_FILE"
}

function strip_prefix() {
    local raw="$1"
    if [[ -n "$PREFIX" && "$raw" == "$PREFIX"* ]]; then
        echo "${raw#$PREFIX}"
    else
        echo "$raw"
    fi
}

function add_prefix() {
    local raw="$1"
    echo "${PREFIX}${raw}"
}

function bump_version() {
    local part="$1"
    local raw_version
    raw_version=$(read_version)
    local v
    v=$(strip_prefix "$raw_version")

    IFS='.-' read -r major minor patch pre <<< "$v"
    major=${major:-0}
    minor=${minor:-0}
    patch=${patch:-0}

    case "$part" in
        major)
            major=$((major + 1))
            minor=0
            patch=0
            pre=""
            ;;
        minor)
            minor=$((minor + 1))
            patch=0
            pre=""
            ;;
        patch)
            patch=$((patch + 1))
            pre=""
            ;;
        prerelease)
            if [[ -z "$pre" ]]; then
                pre="rc.1"
            else
                prefix="${pre%%[0-9]*}"
                number="${pre##*.}"
                if [[ "$number" =~ ^[0-9]+$ ]]; then
                    number=$((number + 1))
                else
                    number=1
                fi
                pre="${prefix}${number}"
            fi
            ;;
        # TODO: Add a release to remove the prerelease part
        *)
            echo "Invalid bump part: $part"
            usage
            ;;
    esac

    if [[ $PRERELEASE -eq 1 ]] && [[ "$part" != "prerelease" ]]; then
                pre="rc.1"
    fi

    if [[ $RELEASE -eq 1 ]]; then
                pre=""
    fi

    if [[ -n "$pre" ]]; then
        echo "$major.$minor.$patch-$pre"
    else
        echo "$major.$minor.$patch"
    fi
}

CMD=""
CMD_ARGS=()

parse_args "$@"

case "$CMD" in
    show)
        v=$(read_version)
        full=$(add_prefix "$v")
        echo "$full"
        ;;

    set)
        [[ ${#CMD_ARGS[@]} -ne 1 ]] && usage
        raw="${CMD_ARGS[0]}"
        stripped=$(strip_prefix "$raw")
        full=$(add_prefix "$stripped")
        echo "$full"
        [[ $WRITE -eq 1 ]] && write_version "$full"
        ;;

    bump)
        [[ ${#CMD_ARGS[@]} -ne 1 ]] && usage
        part="${CMD_ARGS[0]}"
        result=$(bump_version "$part")
        full=$(add_prefix "$result")
        
        echo "$full"
        [[ $WRITE -eq 1 ]] && write_version "$full"
        ;;

    *)
        usage
        ;;
esac
