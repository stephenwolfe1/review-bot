# Running review-bot with Actions Runner Controller

review-bot is a plain binary that the workflow runs on a self-hosted runner,
which has to be able to reach your model server. With
[Actions Runner Controller](https://github.com/actions/actions-runner-controller)
on Kubernetes:

1. **Runner image.** Add the binary to your runner image.
   [`runner.Dockerfile`](runner.Dockerfile) shows the two lines: a stage from
   `ghcr.io/stephenwolfe1/review-bot` and a `COPY`. The published image is
   multi-arch (amd64 and arm64). The bot only calls the GitHub API and the
   model, so the runner doesn't need Docker-in-Docker.
2. **Scale set.** Install the `gha-runner-scale-set` chart with
   [`values.yaml`](values.yaml) as a starting point. On a personal account,
   scale sets register per repository, so you need one per repo.
3. **Network.** Pods must reach the model server. Pod traffic usually leaves
   with the node's IP, so any firewall in front of the model server has to
   allow the nodes. [`networkpolicy.yaml`](networkpolicy.yaml) restricts
   review-bot runner pods to DNS, the model server and public HTTPS.
4. **Workflow.** Copy [`../../examples/review-bot.yml`](../../examples/review-bot.yml)
   into the repo and create the `review-bot` environment it describes.

Model settings come from the workflow (environment secrets and variables),
not the runner, so one runner image works for every repo.

Only attach self-hosted runners to **private** repositories.
