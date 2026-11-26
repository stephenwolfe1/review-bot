# Example runner image for Actions Runner Controller: the stock runner with
# review-bot added. If you already maintain a custom runner image, add just
# the review-bot stage and the COPY line to it.
ARG RUNNER_VERSION=2.337.0
ARG REVIEW_BOT_VERSION=0.1.0

FROM ghcr.io/stephenwolfe1/review-bot:${REVIEW_BOT_VERSION} AS review-bot

FROM ghcr.io/actions/actions-runner:${RUNNER_VERSION}
COPY --from=review-bot /review-bot /usr/local/bin/review-bot
