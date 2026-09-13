# The image is built by CI and never on the server: the box this runs on has
# under 400 MB of memory free, which is not enough to compile Go. A change to
# any part of the agent is a new image, and an upgrade is running it — nothing
# is ever modified in place.
#
# Two stages. The first compiles the agent and every tool the way `task build`
# does, and fetches the one binary that is not built here. The second carries
# them, the interface, the skills, and the userland the tools need — a shell,
# because tools/bash execs /bin/sh; git and gh, because the agent proposes
# changes to itself by opening a pull request. The sandbox needs nothing here:
# Landlock is the kernel's, and the agent asks for it itself. Nothing else: no
# package manager state, no network client.

FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -o /out/agent ./cmd/agent
# Every tool directory becomes /out/tools/<name>/{manifest.json,run}, which is
# the layout the registry scans. A directory without a manifest is not a tool.
# Everything in a tool's directory ships except its source and its eval cases:
# a tool may carry a schema, a panel, or a file nobody has thought of yet, and
# naming them one by one is how notes reached production without the schema that
# creates its table.
RUN set -eu; \
    for d in tools/*/; do \
      [ -f "$d/manifest.json" ] || continue; \
      name=$(basename "$d"); \
      mkdir -p "/out/tools/$name"; \
      cp -R "$d". "/out/tools/$name/"; \
      rm -f "/out/tools/$name"/*.go "/out/tools/$name/eval.json"; \
      go build -trimpath -o "/out/tools/$name/run" "./$d"; \
    done

# gh comes from its own release rather than an apt repository, so the runtime
# image needs neither the repository nor the network client that would add it.
# Bump the version and both digests together; they are from the release's own
# checksums file.
ARG TARGETARCH=amd64
ARG GH_VERSION=2.100.0
ARG GH_SHA256_amd64=e4d4bb4498e8d007abe545b6568926793ace1b6447da598294a610018cb164be
ARG GH_SHA256_arm64=ea4e7a581a32ccad6cc7923cb1576ac5859ba4b9a16ab22eb8f8a96e78e2e961
RUN set -eu; \
    tarball="gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    case "$TARGETARCH" in \
      amd64) sha="$GH_SHA256_amd64" ;; \
      arm64) sha="$GH_SHA256_arm64" ;; \
      *) echo "no pinned digest for $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSLO "https://github.com/cli/cli/releases/download/v${GH_VERSION}/${tarball}"; \
    echo "${sha}  ${tarball}" | sha256sum -c -; \
    tar -xzf "$tarball" --strip-components=2 -C /out "gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh"

FROM debian:bookworm-slim
# git for the clone and the push. ca-certificates is what makes every outbound
# call verifiable, the model gateway included. The sandbox adds nothing to this
# list — Landlock is a kernel facility the agent asks for directly, which is why
# it works in an unprivileged container where bubblewrap did not.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git \
 && rm -rf /var/lib/apt/lists/*

# The agent runs as one user, and it is not root. The data and workspace
# directories are created here so they are owned by that user before anything
# mounts over them.
RUN useradd --create-home --uid 10001 agent
WORKDIR /app
COPY --from=build /out/agent /app/agent
COPY --from=build /out/gh /usr/local/bin/gh
COPY --from=build /out/tools /app/tools
COPY web /app/web
COPY skills /app/skills
RUN mkdir -p /app/data /app/workspace && chown -R agent:agent /app

USER agent
ENV AGENT_ADDR=:8080 \
    AGENT_DATA=/app/data \
    AGENT_WORKSPACE=/app/workspace \
    AGENT_TOOLS=/app/tools \
    AGENT_SKILLS=/app/skills \
    AGENT_WEB=/app/web \
    AGENT_ENV=/app/data/.env
EXPOSE 8080
# The agent asks itself, over /status, which answers without reaching a model.
# A network client in the image for one request the agent can make of itself is
# a dependency bought for nothing.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD ["/app/agent", "-health"]
ENTRYPOINT ["/app/agent"]
