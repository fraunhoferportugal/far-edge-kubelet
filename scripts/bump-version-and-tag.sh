#!/bin/bash

set -e

TYPE=$1

if [[ -z "$TYPE" ]]; then
  echo "Usage: $0 <patch|minor|major|prerelease>"
  exit 1
fi

if [[ "$(git symbolic-ref --short HEAD)" != "master" ]]; then
  echo "You must be on the 'master' branch to bump the version."
  exit 1
fi

if [[ -n $(git status --porcelain) ]]; then
  echo "Error: Your working directory is dirty. Please commit or stash changes before running this script."
  git status --short
  exit 1
fi

OLD_VERSION=$(cat VERSION)
echo "Bumping $TYPE version (current: $OLD_VERSION)..."

./scripts/semver.sh bump "$TYPE"

NEW_VERSION=$(cat VERSION)
echo "Bumped $TYPE from $OLD_VERSION to $NEW_VERSION"
echo

make authors REF="v$OLD_VERSION"

git diff || true
echo

git add VERSION AUTHORS
git commit -m "chore: bump $TYPE version to v$NEW_VERSION and update AUTHORS"
git tag "v$NEW_VERSION"
echo "Tagged commit as v$NEW_VERSION"

read -p "Push commit and tag to origin? [y/N]? (Default: n) " push_confirm
push_confirm=${push_confirm:-n}
if [[ "$push_confirm" =~ ^[Yy]$ ]]; then
  git push origin master && git push origin "v$NEW_VERSION"
  echo "Pushed to origin"
else
  echo "Skipped push. Commit and tag remain local."
  read -p "Delete tag and erase commit? [y/N]? (Default: y) " revert_confirm
  revert_confirm=${revert_confirm:-y}
  if [[ "$revert_confirm" =~ ^[Yy]$ ]]; then
    git tag -d "v$NEW_VERSION" > /dev/null
    git reset --soft HEAD~1
    git restore --staged VERSION AUTHORS
    git restore VERSION AUTHORS
    echo "Deleted tag, erased commit and restored VERSION file. Everything as before"
  else
    echo "Skipped revert. Commit and tag remain local."
  fi
fi
