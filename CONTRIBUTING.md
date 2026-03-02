# Contributing to the Far-Edge Kubelet

Thanks for wanting to contribute! This file explains the minimal rules we follow so PRs can be reviewed and merged quickly.

## Table of Contents
- [Quick Start](#quick-start)
- [Local Development & Tests](#local-development--tests)
- [Pull Request Workflow](#pr-workflow)
  - [CI Checks (Required)](#ci-checks-required)
  - [Labels](#labels)
- [Version Management](#version-management)
- [Releases](#releases)
- [Issue Reports and Feature Requests](#issue-reports-and-feature-requests)
- [Code of Conduct](#code-of-conduct)

## Quick start
1. Fork the repo and create a branch for your contribution: `git checkout -b [feature|fix|ci]/my-change`
2. Write code against `development`. Prefer small, focused PRs.

## Local Development & Tests
- Language and tools: 
    - Go (>=1.23)
    - `make`
    - bash
    - Docker (only if building images locally),
    - Kubernetes cluster (for testing)
- Build: `make build` / `make build-image`
- Unit tests: `make test`
- Lint: `make lint`
- Formatting: `make format`

## PR workflow
- Open a PR against `development`.
- Use a descriptive title and reference the issue: `Fix: <short desc> (#123)`
- PR body checklist:
    - Code compiles locally
    - Added/updated tests where applicable
    - Unit tests pass
    - Code follows linting/formatting rules (`make lint` and `make format` don't report issues)
- At least two approving review from a maintainer required. Maintainers may request changes — please address them with new commits on the same branch.

### CI checks (required)
All PRs must pass CI before merge:
- `Test and Lint`
    - Lint code and check formatting using `golangci-lint`
    - Unit tests
If a CI job fails, fix the issue or add a clear PR comment explaining why a job can be skipped.

### Labels

This repository uses labels to help with some management tasks.
| Label | Description |
| --- | --- |
| `no-tag` | Keeps workflows from tagging the merge commit in the `main` branch and creating a release draft on pull requests merges targeting main. |
| `needs-triage` | Default label, indicating a maintainer is to correctly tag the issue |

## Version Management

FITA component versioning follows a [semantic versioning (semver)](https://semver.org/) inspired scheme.

Version bumps are automated through Pull Request discussions using the `Tag on PR merged to main` workflow and [RelSync](https://github.com/fraunhoferportugal/RelSync).
When a Pull Request targeting the `main` branch is merged and the `no-tag` label is not present, RelSync will be invoked to create and push a new tag pointing to the latest commit in the `main` branch (the merge commit).

The workflow parses the version bump type using the **latest PR discussion comment** with the following syntax:
```
!relsync bump (major|minor|patch) 
```
If no comment is found, `patch` is assumed.

## Releases
Far-Edge Kubelet releases are handled by the `Release` workflow. 
This workflow is triggered either by a tag being pushed to the repository,
by the `Tag on PR merged to main` workflow,
or manually via a workflow dispatch using an existing tag as its argument.

The workflow will validate the provided tag is a valid SemVer string,
run the `Test and Lint` workflow against the tagged commit,
and, finally, use [GoReleaser](https://goreleaser.com/) to build binaries and
container images for the supported platforms (amd64, arm64 and armv7),
create a draft release in GitHub with the built artifacts.

## Issue reports and Feature Requests
When opening an issue make sure you are targeting the correct FITA sub-repo. You can check them anytime in the [architecture docs](https://fraunhoferportugal.github.io/fita/docs/) and in the main repo in the [`components/` directory](https://github.com/fraunhoferportugal/fita/tree/development/components).

In your issue report, please include the following:
- Reproduction steps
- Version/commit hash
- Relevant logs and config snippets

If you're submitting a feature requst, please include a motivation or use case for your request and, optionally, a solution proposal.
