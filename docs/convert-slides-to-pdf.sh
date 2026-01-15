#!/usr/bin/env bash

# Make script more robust
set -o errexit  # abort on nonzero exitstatus
set -o nounset  # abort on unbound variable
set -o pipefail # don't hide errors within pipes

# Terminal colors
readonly red='\e[31m'
readonly green='\e[32m'
readonly yellow='\e[33m'
readonly reset='\e[0m'

# Functions for output
error() {
  printf "${red}!!! %s${reset}\n" "${*}" 1>&2
}

info() {
  printf "${green}==> %s${reset}\n" "${*}"
}

warn() {
  printf "${yellow}*** %s${reset}\n" "${*}"
}

# Function to check if a command exists
command_exists() {
  local command_name="${1}"
  command -v "${command_name}" >/dev/null 2>&1
}

# Check for required dependencies
check_dependencies() {
  local missing_deps=0

  # Check for npx
  if ! command_exists npx; then
    error "npx is not installed"
    warn "To install npx, you need to install Node.js and npm:"
    warn "We recommend using nvm (Node Version Manager): https://github.com/nvm-sh/nvm"
    warn "Install NVM with: curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.5/install.sh | bash"
    warn "Then install Node.js with: nvm install --lts"
    missing_deps=1
  else
    info "npx is available"
  fi

  # Check for ghostscript (gs)
  if ! command_exists gs; then
    error "ghostscript is not installed"

    # Detect OS and provide installation instructions
    if [[ "$(uname)" == "Darwin" ]]; then
      warn "To install ghostscript on macOS, run: brew install ghostscript"
    elif [[ "$(uname)" == "Linux" ]]; then
      warn "To install ghostscript on Linux, run: sudo apt-get update && sudo apt-get install -y ghostscript"
    else
      warn "Please install ghostscript for your operating system"
    fi

    missing_deps=1
  else
    info "ghostscript is available"
  fi

  # Exit if dependencies are missing
  if [[ "${missing_deps}" -ne 0 ]]; then
    error "Please install the missing dependencies and try again"
    exit 1
  fi
}

# Main function
main() {
  info "Checking dependencies"
  check_dependencies

  info "Converting slides to uncompressed PDF"
  npx -y decktape reveal \
    --chrome-arg=--no-sandbox \
    --chrome-arg=--disable-setuid-sandbox \
    --fragments \
    "_site/presentation/index.html" "ptd-slides-uncompressed.pdf"

  info "Compressing PDF"
  gs -sDEVICE=pdfwrite -d'CompatibilityLevel=1.6' -d'PDFSETTINGS=/ebook' \
    -dNOPAUSE -dQUIET -dBATCH -sOutputFile='ptd-slides.pdf' "ptd-slides-uncompressed.pdf"

  info "PDF conversion complete: ptd-slides.pdf"
}

# Run the main function
main "$@"
