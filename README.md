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

- 🐛 **Bugs and 🔒 security**: one focused prompt each. Every finding needs a
  concrete failing example, and a skeptical second pass checks it.
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
> **Example:** `ParseRatio("3/0")` panics; it should return `ErrZeroDivisor`.
>
> ```suggestion
> 	if y == 0 {
> 		return 0, ErrZeroDivisor
> 	}
> 	return x / y, nil
> ```
> <sub>⚠️ Written by an AI model. Read the change before applying it.</sub>
>
> <sub>review-bot · qwen3-coder:30b</sub>

## Contents

- [How it works](#how-it-works)
- [Quick start](#quick-start)
- [Install](#install)
- [Model providers and fallback](#model-providers-and-fallback)
- [Performance](#performance)
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
4. **Filter.** Every finding must come with a failing example. A bug's
   example must name a specific value (a number, a quoted string, nil,
   empty, …) or the finding is dropped. One worded as "may" or "could" is
   capped at Medium. Security examples are naturally worded that way ("an
   attacker could…"), so they only need to be present. These checks are done
   in code, not left to the model. Findings within 2 lines of each other are
   merged, even when the bugs and security checks both flagged the line. So are low-confidence findings, lines outside the
   diff, and duplicates, including anything already raised (resolved threads
   count).
5. **Verify.** A second model call per finding writes its analysis of the
   example first, then decides whether the problem is real. A wrong
   suggestion only removes the suggestion, never the finding. For files too big to send whole, it also gets the
   definitions the finding names from elsewhere in the file, so it can check
   claims like "this call doesn't match the function". It sees exactly which lines a suggested
   change would replace, and drops suggestions that are wrong or would remove
   needed code. It can lower a finding's severity, never raise it.
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
| `num_ctx` | Ollama context window (default: `LLM_NUM_CTX`, or the profile's) |
| `num_batch` | Ollama: prompt tokens processed at once (Ollama's default 512). Higher can read prompts faster. |
| `temperature` | default 0.1 for Ollama. Other APIs use the provider's default, since some newer hosted models reject custom values. |
| `think` | Ollama only: `true` turns a thinking model's reasoning on (default: `LLM_THINK`, which is off) |
| `max_tokens` | output limit per reply. Ollama: 2048 with `think: false`, otherwise 8192. Anthropic: 8192. |
| `json_mode` | OpenAI-compatible only. Sends `response_format` (default true) and turns it off automatically if the server rejects it. |

`api_key` also works, but keep keys out of the JSON and pass each one as its
own secret.

</details>

> **A hosted provider receives your code.** List it last if you only want it
> when your own servers are down.

## Performance

A review makes one model call per changed file per check, plus one per finding
to verify it. Each line in the log shows a call's prompt and output tokens with
speeds, so you can see where the time goes:

```
review-bot: internal/review/review.go [bugs]: waiting on the model…
review-bot:   ollama@gpu-box:11434 (qwen3-coder:30b): prompt 9120 tok (1450 tok/s), output 212 tok (71 tok/s) in 9.4s
```

If it's slow:

- **Thinking is off by default.** Reasoning models (Qwen3, DeepSeek-R1, …)
  would otherwise write thousands of hidden tokens before every answer, which
  makes a review many times slower. `LLM_THINK=true` turns it back on. It's
  slower, and can catch subtler bugs. The log shows "chars of thinking" when
  a model is thinking.
- **Prefer a mixture-of-experts coding model.** `qwen3-coder:30b` activates
  only about 3B parameters per token, so it usually generates faster than a
  dense 7–9B model. It also doesn't think, and it's better at code. It needs
  about 20 GB of memory.
- **Use a 4-bit quantization** if the model offers one: generation speed is
  mostly limited by memory bandwidth, so a smaller model file is faster.
- **Use `REVIEW_PROFILE=lite`** on a laptop or with a small model: less
  context per file and fewer, more serious findings. While testing,
  `REVIEW_CHECKS=bugs` makes fewer calls.
- **Replies are constrained to a schema.** With Ollama, the bot sends the JSON
  schema of the reply it expects, so the model can only write a reply of that
  shape and stops when it's complete. Older Ollama versions that don't accept
  schemas fall back to plain JSON mode automatically.
- **Try a larger `LLM_NUM_BATCH`** (1024 or 2048) if reading prompts is the
  slow part (low "prompt tok/s" on each file's first check). It uses a little
  more memory. Ollama reloads the model when this changes, so use one value
  for everything that shares the server.
- **Runaway replies are capped.** Some models keep writing after their answer,
  and Ollama has no limit by default, so one call could run until the
  timeout. The bot caps each reply (`LLM_MAX_TOKENS`). When a reply hits the
  cap, it keeps the answer if the JSON is complete, and otherwise skips that
  call with a warning.
- **Watch for the context warning.** If a prompt is larger than `LLM_NUM_CTX`,
  Ollama silently drops its start and the model reviews part of the file. The
  bot logs a warning when that happens. Raise `LLM_NUM_CTX` or lower
  `REVIEW_FULL_FILE_LINES`.

Every call about a file (each check, then each finding's verification)
sends the same prompt up to its instructions, and they run back to back. So
Ollama reads the file once, reuses its cached reading for the rest, and each
later call starts almost straight away.

## Configuration

Everything is set through environment variables. Most setups need only the
GitHub token, a model server and, optionally, a profile:

```yaml
env:
  GITHUB_TOKEN: ${{ github.token }}
  LLM_BASE_URL: ${{ secrets.LLM_BASE_URL }}
  REVIEW_PROFILE: standard
```

### Profiles

`REVIEW_PROFILE` sets the review settings below as a group. Any of them set
on its own still overrides the profile.

| | `lite` | `standard` (default) | `full` |
|---|---|---|---|
| For | laptops, small models, quick feedback | most setups | a strong model with time to spare |
| `REVIEW_FULL_FILE_LINES` | 200 | 500 | 1500 |
| `LLM_NUM_CTX` (Ollama) | 16384 | 16384 | 32768 |
| `REVIEW_MAX_FILES` | 20 | 40 | 100 |
| `REVIEW_MAX_PATCH_LINES` | 400 | 800 | 2000 |
| `REVIEW_MAX_COMMENTS` | 8 | 15 | 25 |
| `REVIEW_MIN_SEVERITY` | medium | low | low |
| `REVIEW_MIN_CONFIDENCE` | 0.8 | 0.7 | 0.6 |
| `REVIEW_VERIFY` | on | on | on |

Every profile runs both checks (bugs and security) and verifies each finding.

### All settings

| Variable | Default | |
|---|---|---|
| `GITHUB_TOKEN` | | required; the workflow's token is enough |
| `LLM_PROVIDERS` | | JSON list of providers; overrides the `LLM_*` variables below |
| `LLM_API` | `ollama` | any `api` value from the table above |
| `LLM_BASE_URL` | the API's default | comma-separated to try several servers in order |
| `LLM_MODEL` | `qwen3-coder:30b` for Ollama | required for other APIs |
| `LLM_API_KEY` | | key for the single provider |
| `LLM_NUM_CTX` | from the profile | Ollama context window, for every Ollama provider without its own `num_ctx`. Ollama's own default is too small for whole-file context. |
| `LLM_TEMPERATURE` | | |
| `LLM_THINK` | `false` | `true` turns on "thinking" for Ollama models that do it (Qwen3, DeepSeek-R1, …): slower, sometimes more thorough. Also applies to Ollama entries in `LLM_PROVIDERS` without their own `think`. |
| `LLM_NUM_BATCH` | Ollama's default (512) | see `num_batch`; also applies to `LLM_PROVIDERS` entries without their own |
| `LLM_MAX_TOKENS` | see `max_tokens` | output limit per reply, for the single provider and for `LLM_PROVIDERS` entries without their own |
| `LLM_TIMEOUT` | `10m` | per model call. Connecting to a server times out after 5s. |
| `REVIEW_PROFILE` | `standard` | `lite`, `standard` or `full` ([Profiles](#profiles)) |
| `REVIEW_CHECKS` | `bugs,security` | |
| `REVIEW_MIN_SEVERITY` | from the profile | `low`, `medium`, `high`, `critical` |
| `REVIEW_MIN_CONFIDENCE` | from the profile | the model's own 0–1 confidence |
| `REVIEW_VERIFY` | from the profile | second-opinion pass per finding |
| `REVIEW_MAX_COMMENTS` | from the profile | per run, highest severity first |
| `REVIEW_MAX_FILES` | from the profile | |
| `REVIEW_MAX_PATCH_LINES` | from the profile | larger file diffs are skipped and listed in the summary |
| `REVIEW_FULL_FILE_LINES` | from the profile | send the whole file as context up to this size |
| `REVIEW_IGNORE` | | extra comma-separated globs, e.g. `generated/**,**/*_test.go`. Lockfiles, images, `vendor/` and `.pbxproj` are always skipped. |
| `REVIEW_TESTS` | `false` | `true` also reviews test code (`*_test.go`, `*.test.*`, `*.spec.*`, `test_*.py`, `tests/`, `__tests__/`, `testdata/`), which is skipped by default |
| `REVIEW_DOCS` | `false` | `true` also reviews documentation (`*.md`, `*.rst`, `docs/`, `LICENSE`), which is skipped by default |
| `REVIEW_FAIL_ON` | | e.g. `high`: fail the check while open findings at or above it exist. A quality gate, not a security control; see [Security](#security). |
| `REVIEW_BOT_LOGIN` | `github-actions[bot]` | the account the bot posts as; only its comments are trusted. Set it if you run the bot with another token, e.g. `my-app[bot]`. |
| `REVIEW_DRAFTS` | `false` | review draft PRs |
| `REVIEW_FORCE` / `-force` | `false` | review even if the head commit was already reviewed |
| `DRY_RUN` / `-dry-run` | `false` | log instead of posting |

Inside GitHub Actions the repository and PR number come from the event. Run
locally, they come from `-repo owner/name` and `-pr N`.

## Security

To report a vulnerability, see [SECURITY.md](SECURITY.md). Please don't open
a public issue.

- **Only attach self-hosted runners to private repositories.** A runner on your
  network shouldn't take jobs from untrusted code. The example workflow also
  skips PRs from forks.
- **Least privilege.** The workflow needs only `contents: read` and
  `pull-requests: write`. The bot never checks out or runs the PR's code. It
  reads diffs and files through the API.
- **The model's output is untrusted.** A PR can try to steer the model with
  instructions hidden in code or comments (prompt injection). The prompts tell
  the model to ignore them, and the bot limits what a steered model can do:
  - its output is only ever posted as comments, never executed
  - mentions, links, images and raw HTML in its text are made inert before
    posting
  - suggested changes are marked as AI-written and dropped when much larger
    than the lines they replace
- **Forged markers are ignored.** The bot keeps its state in hidden markers,
  and trusts them only in comments by its own account (`REVIEW_BOT_LOGIN`).
  Anyone else's copy of a marker has no effect.
- **Secrets.** Keep model URLs and API keys in environment secrets
  (see [the example workflow](examples/review-bot.yml)). Keys are never
  logged.
- **Know what it can't stop.** Prompt injection can still make the bot
  report nothing, or resolve a thread that wasn't really fixed. Treat it as an
  extra reviewer, not as a security gate. `REVIEW_FAIL_ON` can be switched off
  the same way, so don't rely on it to keep vulnerable code out.
- **Anyone who can push a branch is trusted with the runner.** On
  `pull_request`, GitHub runs the workflow file from the PR itself, so a PR
  can change it to run any command on your runner, with the `review-bot`
  environment's secrets. Fork PRs are skipped. Branches in the repository
  aren't, so only give push access to people you'd trust with the runner and
  those secrets.
- **Network.** [deploy/arc/networkpolicy.yaml](deploy/arc/networkpolicy.yaml)
  limits runner pods to DNS, the model server and public HTTPS.

## Limitations

- Models miss things and sometimes invent problems. The verify pass and the
  confidence threshold reduce the noise, but treat this as a first pass, not a
  replacement for human review.
- Context is the changed file. Bugs that depend on code elsewhere in the
  repository are mostly out of reach.
- Test code and documentation are skipped by default (`REVIEW_TESTS`,
  `REVIEW_DOCS`). Models tend to judge test fakes as if they were production
  code.
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

1. Commit to `main` with
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
