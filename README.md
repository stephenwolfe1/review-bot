# review-bot

[![ci](https://github.com/stephenwolfe1/review-bot/actions/workflows/ci.yml/badge.svg)](https://github.com/stephenwolfe1/review-bot/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/stephenwolfe1/review-bot?sort=semver)](https://github.com/stephenwolfe1/review-bot/releases)
[![go](https://img.shields.io/github/go-mod/go-version/stephenwolfe1/review-bot)](go.mod)
[![license](https://img.shields.io/github/license/stephenwolfe1/review-bot)](LICENSE)

An AI pull request reviewer for GitHub Actions that runs on **your own model**.
It's similar to Copilot code review or Bugbot, but the model runs on hardware
you control (Ollama or any OpenAI-compatible server), so your code goes only
to GitHub and your own servers. Hosted providers such as Anthropic and OpenAI
are supported as a fallback.

- 🐛 **Bugs and 🔒 security**: one focused prompt each, plus a skeptical second
  pass that drops false positives.
- 💬 **Inline comments with fixes**: severity-labelled comments and, when the
  fix is small and certain, a one-click GitHub suggested change.
- ✅ **Resolves its own comments**: when a later commit fixes the problem, it
  replies with the commit and resolves the thread. Unfixed threads stay open.
- 🔁 **Incremental and quiet**: reviews only what changed since its last run and
  never repeats itself. Resolve a thread yourself to dismiss it for good.
- 🧭 **Fallback**: tries model servers in order and moves on when one is down.
- 📦 **One static binary**: Go standard library only, no database. Its state
  lives in hidden markers in its own comments.

A comment looks like this:

> 🔴 **High · Bugs** — Division by zero when y is 0
>
> `Div` panics when `y` is 0, which `ParseRatio` passes through from user input.
>
> ```suggestion
> 	if y == 0 {
> 		return 0, ErrZeroDivisor
> 	}
> 	return x / y, nil
> ```
>
> <sub>review-bot · qwen3-coder:30b</sub>

## Contents

- [How it works](#how-it-works)
- [Quick start](#quick-start)
- [Install](#install)
- [Model providers and fallback](#model-providers-and-fallback)
- [Configuration](#configuration)
- [Security](#security)
- [Limitations](#limitations)
- [Development](#development)
- [License](#license)

## How it works

Each run, triggered by a push to a pull request:

1. **Load state.** Read the PR, the bot's own review threads, and its summary
   comment, which records the last reviewed commit.
2. **Resolve.** For each open bot thread in a file that changed since then, ask
   the model whether the new code fixes the finding. If it does, reply
   "✅ Addressed in `abc1234`" and resolve the thread.
3. **Review.** For each changed file, send the line-numbered diff plus the whole
   file as context, once per check (bugs, security).
4. **Filter.** Drop low-confidence findings, lines outside the diff, and
   duplicates, including anything already raised (resolved threads count).
5. **Verify.** A second model call per finding rejects false positives and bad
   suggestions.
6. **Post.** One review with inline comments, and an updated summary comment
   with counts of open issues by severity.

Force-pushes are detected and trigger a full review. Duplicate detection still
prevents repeat comments. Draft, closed and merged PRs are skipped.

## Quick start

You need a model server, a self-hosted runner that can reach it, and a
workflow.

**1. Model.** On a machine with a GPU:

```
ollama pull qwen3-coder:30b
```

A 30B model at Q4 with a 16k context fits a 24 GB GPU. Smaller models work but
produce more noise. Set `OLLAMA_KEEP_ALIVE=30m` so the model stays loaded
between files.

**2. Try it locally.** This reviews a real PR, prints the comments it would
post, and posts nothing. It needs the [GitHub CLI](https://cli.github.com) for
a token.

```
make build
GITHUB_TOKEN=$(gh auth token) LLM_BASE_URL=http://localhost:11434 \
  bin/review-bot -repo owner/repo -pr 12 -dry-run -force
```

**3. Runner.** Install `review-bot` on a self-hosted runner that can reach the
model server ([Install](#install)). For Actions Runner Controller on Kubernetes,
see [deploy/arc](deploy/arc/README.md).

**4. Workflow.** Copy [examples/review-bot.yml](examples/review-bot.yml) to
`.github/workflows/` in the repository to review. Then create the `review-bot`
environment it describes, with the model server's address as a secret.

## Install

| | |
|---|---|
| Container image (linux/amd64, linux/arm64) | `ghcr.io/stephenwolfe1/review-bot`, containing only `/review-bot` on `distroless/static` |
| Binaries (Linux, macOS) | attached to each [release](https://github.com/stephenwolfe1/review-bot/releases), with `checksums.txt` |
| From source (Go 1.27.4+) | `make build` → `bin/review-bot` |

To add it to a runner image, copy the binary out of the published image:

```dockerfile
FROM ghcr.io/stephenwolfe1/review-bot:0.1.0 AS review-bot
FROM ghcr.io/actions/actions-runner:2.338.2
COPY --from=review-bot /review-bot /usr/local/bin/review-bot
```

## Model providers and fallback

**One or more Ollama servers** with the same model, tried in order:

```
LLM_BASE_URL=http://gpu-box:11434,http://backup:11434
LLM_MODEL=qwen3-coder:30b
```

**Different models or hosted providers:** set `LLM_PROVIDERS` to a JSON list,
also tried in order:

```json
[
  {"api": "ollama", "base_url": "http://gpu-box:11434", "model": "qwen3-coder:30b"},
  {"api": "ollama", "base_url": "http://laptop:11434", "model": "qwen3.5:9b"},
  {"api": "anthropic", "model": "claude-sonnet-5-5"}
]
```

Failover works like this:

- Before the review starts, each Ollama server is probed and must have the model
  pulled. Hosted providers without an API key are skipped.
- During the run, a provider that stops answering (connection error, 5xx, 429
  after retries, 401/403/404) is dropped and the next one takes over.
- If a provider rejects a single request (another 4xx, such as a prompt that's
  too long), only that request moves on to the next provider.
- Comments and the summary name the model that was used.

| `api` | Default base URL | API key from |
|---|---|---|
| `ollama` | `http://localhost:11434` | none (set `api_key_env` if it's behind auth) |
| `openai` | `https://api.openai.com/v1` | `OPENAI_API_KEY`, or none when `base_url` points at your own server (vLLM, llama.cpp, LM Studio) |
| `anthropic` | `https://api.anthropic.com` | `ANTHROPIC_API_KEY` |
| `gemini` | Gemini's OpenAI-compatible endpoint | `GEMINI_API_KEY` |
| `openrouter` | `https://openrouter.ai/api/v1` | `OPENROUTER_API_KEY` |
| `groq` | `https://api.groq.com/openai/v1` | `GROQ_API_KEY` |
| `mistral` | `https://api.mistral.ai/v1` | `MISTRAL_API_KEY` |
| `deepseek` | `https://api.deepseek.com/v1` | `DEEPSEEK_API_KEY` |
| `xai` | `https://api.x.ai/v1` | `XAI_API_KEY` |

<details>
<summary>All provider fields</summary>

| Field | |
|---|---|
| `api` | one of the values above (default `ollama`) |
| `model` | required |
| `base_url` | overrides the default |
| `name` | label for logs (default `api@host`) |
| `api_key_env` | read the key from this variable instead of the default |
| `num_ctx` | Ollama context window (default 16384) |
| `temperature` | default 0.1 for Ollama. Other APIs use the provider's default, since some newer hosted models reject custom values. |
| `max_tokens` | Anthropic only (default 8192) |
| `json_mode` | OpenAI-compatible only. Sends `response_format` (default true) and turns it off automatically if the server rejects it. |

`api_key` also works, but keep keys out of the JSON and pass each one as its
own secret.

</details>

> **A hosted provider receives your code.** List it last if you only want it
> when your own servers are down.

## Configuration

Everything is set through environment variables.

| Variable | Default | |
|---|---|---|
| `GITHUB_TOKEN` | | required; the workflow's token is enough |
| `LLM_PROVIDERS` | | JSON list of providers; overrides the `LLM_*` variables below |
| `LLM_API` | `ollama` | any `api` value from the table above |
| `LLM_BASE_URL` | the API's default | comma-separated to try several servers in order |
| `LLM_MODEL` | `qwen3-coder:30b` for Ollama | required for other APIs |
| `LLM_API_KEY` | | key for the single provider |
| `LLM_NUM_CTX` | `16384` | Ollama context window. Ollama's own default is too small for whole-file context. |
| `LLM_TEMPERATURE` | | |
| `LLM_TIMEOUT` | `10m` | per model call. Connecting to a server times out after 5s. |
| `REVIEW_CHECKS` | `bugs,security` | |
| `REVIEW_MIN_SEVERITY` | `low` | `low`, `medium`, `high`, `critical` |
| `REVIEW_MIN_CONFIDENCE` | `0.7` | the model's own 0–1 confidence |
| `REVIEW_VERIFY` | `true` | second-opinion pass per finding |
| `REVIEW_MAX_COMMENTS` | `15` | per run, highest severity first |
| `REVIEW_MAX_FILES` | `40` | |
| `REVIEW_MAX_PATCH_LINES` | `800` | larger file diffs are skipped and listed in the summary |
| `REVIEW_FULL_FILE_LINES` | `500` | send the whole file as context up to this size |
| `REVIEW_IGNORE` | | extra comma-separated globs, e.g. `docs/**,CHANGELOG.md`. Lockfiles, images, `vendor/` and `.pbxproj` are always skipped. |
| `REVIEW_FAIL_ON` | | e.g. `high`: fail the check while open findings at or above it exist |
| `REVIEW_DRAFTS` | `false` | review draft PRs |
| `REVIEW_FORCE` / `-force` | `false` | review even if the head commit was already reviewed |
| `DRY_RUN` / `-dry-run` | `false` | log instead of posting |

Inside GitHub Actions the repository and PR number come from the event. Run
locally, they come from `-repo owner/name` and `-pr N`.

## Security

- **Only attach self-hosted runners to private repositories.** A runner on your
  network shouldn't take jobs from untrusted code. The example workflow also
  skips PRs from forks.
- **Least privilege.** The workflow needs only `contents: read` and
  `pull-requests: write`. The bot never checks out or runs the PR's code. It
  reads diffs and files through the API.
- **Untrusted input.** PR content is treated as data, and the prompts tell the
  model to ignore instructions inside it. The model's output is only ever
  posted as comments, never executed.
- **Secrets.** Keep model URLs and API keys in environment secrets
  (see [the example workflow](examples/review-bot.yml)). Keys are never
  logged.
- **Network.** [deploy/arc/networkpolicy.yaml](deploy/arc/networkpolicy.yaml)
  limits runner pods to DNS, the model server and public HTTPS.

## Limitations

- Models miss things and sometimes invent problems. The verify pass and the
  confidence threshold reduce the noise, but treat this as a first pass, not a
  replacement for human review.
- Context is the changed file. Bugs that depend on code elsewhere in the
  repository are mostly out of reach.
- Resolving threads uses `GITHUB_TOKEN` with `pull-requests: write`. If GitHub
  ever refuses that mutation for the Actions token, use a GitHub App token
  instead.

## Development

```
make check   # gofmt, go vet, tests with -race
make build   # bin/review-bot
make image   # local container image
```

The tests run complete review cycles against a fake GitHub and fake model
servers. They cover posting, deduplication, resolving fixed threads, keeping
unfixed ones open, and provider failover.

**Releases** use [release-please](https://github.com/googleapis/release-please):

1. Commit to `develop` with
   [conventional commit](https://www.conventionalcommits.org/) messages
   (`feat:`, `fix:`, …).
2. release-please keeps a release PR open with the next version and
   `CHANGELOG.md`.
3. Merging it tags `vX.Y.Z`.
   [release-please.yml](.github/workflows/release-please.yml) then publishes
   the image and attaches the binaries to the release.

Dependabot keeps actions, Go and base images up to date.

## License

[MIT](LICENSE)
