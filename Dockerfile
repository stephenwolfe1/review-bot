# syntax=docker/dockerfile:1
# Minimal image containing only the review-bot binary. To use it on a
# self-hosted runner, copy the binary into your runner image (see
# deploy/arc/runner.Dockerfile).
FROM --platform=$BUILDPLATFORM golang:1.27.4 AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/review-bot ./cmd/review-bot

# distroless/static: CA certificates (needed for HTTPS to GitHub), tzdata and
# a nonroot user, and no shell or package manager.
FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.source="https://github.com/stephenwolfe1/review-bot" \
      org.opencontainers.image.description="Pull request reviewer that uses a local model" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/review-bot /review-bot
ENTRYPOINT ["/review-bot"]
