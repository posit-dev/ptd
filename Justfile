# Test and build all
all: deps check test build

# Check all
[group('check')]
check: check-python-pulumi

# Format all
[group('format')]
format: format-python-pulumi

alias fmt := format

# Build all
[group('build')]
build: build-cmd

# Test all
[group('test')]
test: test-python-pulumi test-lib test-cmd

# Run the ptd CLI
ptd *ARGS:
  cd {{ justfile_directory() }}/cmd && go run . {{ARGS}}

write-kubeconfig cluster_dir=invocation_directory() kubeconfig='./kubeconfig':
  #!/usr/bin/env bash
  set -o errexit
  set -o pipefail

  CLUSTER_RELEASE="$(basename '{{ cluster_dir }}')"
  CLUSTER_RELEASE="${CLUSTER_RELEASE##cluster_}"
  WORKLOAD="$(basename "$(dirname '{{ cluster_dir }}')")"
  cd {{ invocation_directory() }}
  aws eks update-kubeconfig \
    --name="default_${WORKLOAD}-${CLUSTER_RELEASE}-control-plane" \
    --kubeconfig='{{ kubeconfig }}' \
    --alias="${WORKLOAD}-${CLUSTER_RELEASE}"

# ----------------------------------------------------------------------------

# ensure git is set up to use ssh
git-ssh:
  #!/bin/bash
  set -xe
  if [ ! -e "$HOME/.gitconfig" ] || [ ! "$(grep $HOME/.gitconfig -e 'url "ssh://git@github.com"')" ]; then
    echo -e '[url "ssh://git@github.com"]\n\tinsteadOf = https://github.com' >> $HOME/.gitconfig
  fi;

# unset all aws variables
aws-unset:
  unset AWS_DEFAULT_PROFILE
  unset AWS_DEFAULT_REGION
  unset AWS_SESSION_TOKEN
  unset AWS_SECRET_ACCESS_KEY
  unset AWS_ACCESS_KEY_ID

############################################################################
# Setup and dependencies
############################################################################
alias link-bins := symlink-binaries

deps: python-deps check-session-manager-plugin symlink-binaries install-thumbprint

# install python dependencies (uv handles this automatically, but here if you need it)
python-deps:
  uv --directory python-pulumi sync

symlink-binaries:
  #!/usr/bin/env bash
  binlocal="{{ justfile_directory() }}/.local/bin"
  mkdir -p $binlocal

  # Create symlinks only if they don't already exist
  for binary in aws az pulumi; do
    if [ ! -e "$binlocal/$binary" ]; then
      ln -sf "$(which $binary)" "$binlocal/$binary"
    fi
  done

install-thumbprint:
  #!/usr/bin/env bash
  binlocal="{{ justfile_directory() }}/.local/bin"
  mkdir -p $binlocal
  CGO_ENABLED=0 GOBIN=$binlocal go install github.com/rstudio/goex/cmd/thumbprint@latest

check-session-manager-plugin:
  #!/bin/bash
  if ! session-manager-plugin --version &>/dev/null; then
      printf '\n# ---> ERROR: missing "session-manager-plugin"\n' >&2
      printf '#      Please install the AWS Session Manager Plugin:\n#\n' >&2
      printf '#      https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html\n' >&2
      exit 1
  fi

# install git hooks
install-git-hooks:
  #!/usr/bin/env bash
  hooks_dir="{{ justfile_directory() }}/.git/hooks"
  if [ ! -d "$hooks_dir" ]; then
    echo "Error: .git/hooks directory not found. Are you in a git repository?"
    exit 1
  fi

  # Copy the post-checkout hook
  cp "{{ justfile_directory() }}/scripts/post-checkout" "$hooks_dir/post-checkout"
  chmod +x "$hooks_dir/post-checkout"
  echo "Git hooks installed successfully!"

############################################################################
# Test targets
############################################################################

[group('test')]
test-python-pulumi *ARGS:
  cd {{ justfile_directory() }}/python-pulumi && just test {{ARGS}}

[group('test')]
test-cmd *ARGS:
  cd {{ justfile_directory() }}/cmd && go test ./... {{ARGS}}

[group('test')]
test-cmd-cover:
  cd {{ justfile_directory() }}/cmd && go test ./... -cover -coverprofile={{ justfile_directory() }}/cmd/coverage.out

[group('test')]
test-lib *ARGS="./...":
  cd {{ justfile_directory() }}/lib && go test {{ARGS}}

[group('test')]
test-lib-cover:
  cd {{ justfile_directory() }}/lib && go test ./... -cover -coverprofile={{ justfile_directory() }}/lib/coverage.out

[group('test')]
test-e2e URL="":
  #!/usr/bin/env bash
  set -e

  # If URL is provided as argument, use it
  if [[ -n "{{ URL }}" ]]; then
    # Add https:// if no protocol is specified
    TEST_URL="{{ URL }}"
    if [[ ! "$TEST_URL" =~ ^https?:// ]]; then
      TEST_URL="https://${TEST_URL}"
    fi
    echo "Running e2e tests against external URL: $TEST_URL"
    cd {{ justfile_directory() }}
    uv run --project e2e pytest -m "e2e" --base-url="$TEST_URL" ./e2e
    exit 0
  fi

  # If PLAYWRIGHT_TEST_BASE_URL is set, run tests against that URL
  if [[ -n "${PLAYWRIGHT_TEST_BASE_URL:-}" ]]; then
    echo "Running e2e tests against external URL: $PLAYWRIGHT_TEST_BASE_URL"
    cd {{ justfile_directory() }}
    uv run --project e2e pytest -m "e2e" --base-url="$PLAYWRIGHT_TEST_BASE_URL" ./e2e
    exit 0
  fi

  # No URL provided - show usage
  echo "Error: No URL provided for e2e tests."
  echo ""
  echo "Usage:"
  echo "  just test-e2e <url>                    # Test against a specific URL"
  echo "  just test-e2e ganso01-staging.posit.team  # Example"
  echo ""
  echo "Or set PLAYWRIGHT_TEST_BASE_URL environment variable."
  exit 1

############################################################################
# Build targets
############################################################################

alias cli := build-cmd

[group('build')]
build-cmd:
  mkdir -p .local/bin
  goreleaser build --single-target --snapshot --clean -o .local/bin/ptd

#############################################################################
# Check targets
#############################################################################

[group('check')]
check-python-pulumi:
  cd {{ justfile_directory() }}/python-pulumi && just check

#############################################################################
# Format targets
#############################################################################

[group('format')]
format-python-pulumi:
  cd {{ justfile_directory() }}/python-pulumi && just format
