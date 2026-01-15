# Contributing

Below are some helpful directions on getting your environment set up as well as contributing guidelines.

Tooling:
- `snyk` CLI
- local installation of `rskey`
- local installation of `kustomize`
- local installation of `pulumi` and `python3`
- local installation of `golang`

## Setting up `snyk`

It is helpful to be able to run `snyk` locally for development (particularly if a PR fails the `snyk` test).

> Our expectation is that `snyk` would be passing before merging a given PR

1. Install the `snyk` CLI. On Mac systems, you can run `brew install snyk-cli`

2. Run `snyk auth`. This authenticates your local CLI with your SSO
credentials. We also have a `just snyk-auth` for this as well.

3. Run `snyk monitor --org=posit-team-dedicated --all-projects
--policy-path=.snyk` from the root directory.  We also have a `just snyk`
target to do this for you.
