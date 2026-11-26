# Security policy

## Reporting a vulnerability

Please **don't open a public issue** for security problems. Report them
privately through GitHub's
[private vulnerability reporting](https://github.com/stephenwolfe1/review-bot/security/advisories/new)
(**Security → Report a vulnerability** on the repository page).

Please include:

- what the problem is and what an attacker could do with it
- steps or a proof of concept to reproduce it
- the version (`review-bot -version`) and how you run it: image or binary,
  and which model API

This is a one-person project, so responses are best effort. I'll acknowledge
your report, keep you updated while it's being fixed, and credit you in the
advisory unless you'd rather not be named.

## Supported versions

Only the latest release gets security fixes. Fixes ship as a new release,
published as both the container image and the binaries.

## Scope

In scope:

- the `review-bot` binary and the published container image
- PR content (code, titles, descriptions) that makes the bot do more than post
  review comments: for example, run commands, write to the repository, or
  leak `GITHUB_TOKEN`, API keys or other secrets into comments or logs
- the example workflow and deployment files, where following them as written
  leaves a deployment exposed

Out of scope:

- review quality, such as missed bugs, false positives or odd suggestions.
  Open a normal issue for those.
- vulnerabilities in model servers or hosted providers (Ollama, OpenAI-compatible
  servers, Anthropic and others). Report those to their maintainers.
- self-hosted runners attached to public repositories, against the advice in
  the [README](README.md#security)
