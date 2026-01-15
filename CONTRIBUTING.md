# Contributing

Below are some helpful directions on getting your environment set up as well as contributing guidelines.

## Prerequisites

- [Go](https://golang.org/dl/) 1.21+
- [Python](https://www.python.org/downloads/) 3.12+
- [uv](https://github.com/astral-sh/uv) (Python package manager)
- [Pulumi](https://www.pulumi.com/docs/get-started/install/)
- [just](https://github.com/casey/just) (command runner)
- `snyk` CLI (optional, for security scanning)

## Commit Message Format

This project uses [Conventional Commits](https://www.conventionalcommits.org/) for automatic versioning and changelog generation.

### Format

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

### Types

| Type | Description | Version Bump |
|------|-------------|--------------|
| `feat` | New feature | Minor (0.X.0) |
| `fix` | Bug fix | Patch (0.0.X) |
| `docs` | Documentation only | None |
| `style` | Code style (formatting, etc.) | None |
| `refactor` | Code change that neither fixes nor adds | None |
| `perf` | Performance improvement | Patch |
| `test` | Adding/updating tests | None |
| `chore` | Maintenance tasks | None |

### Breaking Changes

For breaking changes, add `!` after the type or include `BREAKING CHANGE:` in the footer:

```
feat!: remove deprecated API endpoint

BREAKING CHANGE: The /v1/legacy endpoint has been removed.
```

Breaking changes trigger a major version bump (X.0.0).

### Examples

```
feat(cli): add proxy command for cluster access
fix(aws): correct IAM policy for EKS access
docs: update installation instructions
chore: update dependencies
```

## Releases

Releases are automated via GitHub Actions. When commits are pushed to `main`:

1. [semantic-release](https://semantic-release.gitbook.io/) analyzes commits
2. If releasable changes exist, a new version is determined
3. CHANGELOG.md is updated
4. A GitHub release is created with the new tag
5. CLI binaries are built and attached to the release

## Setting up `snyk`

It is helpful to be able to run `snyk` locally for development (particularly if a PR fails the `snyk` test).

> Our expectation is that `snyk` would be passing before merging a given PR

1. Install the `snyk` CLI. On Mac systems, you can run `brew install snyk-cli`

2. Run `snyk auth`. This authenticates your local CLI with your credentials.

3. Run `snyk test --all-projects --policy-path=.snyk` from the root directory to check for vulnerabilities.
