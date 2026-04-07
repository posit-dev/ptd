# Test and build all
all: deps check test build

# Check all
[group('check')]
check: check-python-pulumi

# Format all
[group('format')]
format: format-python-pulumi format-go

alias fmt := format

# Build all
[group('build')]
build: cli

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

deps: python-deps check-session-manager-plugin symlink-binaries install-thumbprint install-git-hooks

# install python dependencies (uv handles this automatically, but here if you need it)
python-deps:
  uv --directory python-pulumi sync

symlink-binaries:
  #!/usr/bin/env bash
  binlocal="{{ justfile_directory() }}/.local/bin"
  mkdir -p $binlocal

  # Create wrapper scripts instead of symlinks. Symlinks to shell scripts that use
  # $BASH_SOURCE[0] for self-location (e.g. /usr/bin/az) break when invoked via a
  # symlink path, because bash receives the symlink path instead of the real script path.
  for binary in aws az pulumi; do
    target="$(which $binary)" || { echo "warning: $binary not found in PATH, skipping"; continue; }
    rm -f "$binlocal/$binary"
    printf '#!/usr/bin/env bash\nexec "%s" "$@"\n' "$target" > "$binlocal/$binary"
    chmod +x "$binlocal/$binary"
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

# install git hooks via pre-commit (fails silently in CI)
install-git-hooks:
  -uvx pre-commit install

# configure shell prompt to show ptd workon target
workon-prompt:
  #!/usr/bin/env bash
  set -e

  # Determine which rc file to use
  if [[ -f "$HOME/.zshrc" ]]; then
    RC_FILE="$HOME/.zshrc"
    PROMPT_LINE='PROMPT='"'"'${PTD_WORKON:+[ptd:$PTD_WORKON] }'"'"'"$PROMPT"'
  elif [[ -f "$HOME/.bashrc" ]]; then
    RC_FILE="$HOME/.bashrc"
    PROMPT_LINE='PS1='"'"'${PTD_WORKON:+[ptd:$PTD_WORKON] }'"'"'"$PS1"'
  else
    printf '\033[0;31mError: Neither ~/.zshrc nor ~/.bashrc found\033[0m\n' >&2
    exit 1
  fi

  # Check if already configured
  if grep -q 'PTD_WORKON' "$RC_FILE"; then
    echo "PTD_WORKON prompt already configured in $RC_FILE"
    exit 0
  fi

  # Add the prompt configuration
  echo "" >> "$RC_FILE"
  echo "# ptd workon prompt indicator" >> "$RC_FILE"
  echo "$PROMPT_LINE" >> "$RC_FILE"
  echo "Added PTD_WORKON prompt to $RC_FILE"
  echo "Restart your shell or run: source $RC_FILE"

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

[group('build')]
cli:
  mkdir -p .local/bin
  goreleaser build --single-target --snapshot --clean -o .local/bin/ptd

#############################################################################
# Check targets
#############################################################################

[group('check')]
check-python-pulumi:
  cd {{ justfile_directory() }}/python-pulumi && just check

# Validate Go↔Python config field sync
[group('check')]
validate-config-sync:
  python3 {{ justfile_directory() }}/scripts/validate-config-sync.py

#############################################################################
# Format targets
#############################################################################

[group('format')]
format-python-pulumi:
  cd {{ justfile_directory() }}/python-pulumi && just format

# Format Go code
[group('format')]
format-go:
  cd {{ justfile_directory() }}/lib && go fmt ./...
  cd {{ justfile_directory() }}/cmd && go fmt ./...
