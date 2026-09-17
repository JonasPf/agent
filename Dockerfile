# The image is built by CI and never on the server: the box this runs on has
# under 400 MB of memory free, which is not enough to compile Go. A change to
# any part of the agent is a new image, and an upgrade is running it — nothing
# is ever modified in place.
#
# Two stages. The first compiles the agent and every tool the way `task build`
# does, and fetches the two binaries that are not built here. The second carries
# them, the interface, the skills, and the userland the tools need — a shell,
# because tools/bash execs /bin/sh; git, gh and glab, because the agent proposes
# changes — to itself, and to whatever other repository it is asked to work on —
# by opening a pull request; chromium, because reading most of
# the web means running it; curl, wget, python3 and perl, because a shell is
# only as useful as the programs it can call, and these are the ones reached for
# first. The sandbox needs nothing here: Landlock is the kernel's, and the agent
# asks for it itself. Nothing else: no package manager state.

FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ENV CGO_ENABLED=0
# The version the interface reports. The image carries no repository, so the
# commit being released and the time it was built are stamped into the binary
# here; CI passes the commit it is releasing, which is also the tag a rollback
# names. An unstamped build says "dev" rather than inventing a number.
ARG VERSION=dev
ARG BUILT_AT=
RUN go build -trimpath \
      -ldflags "-X agent/internal/app.Version=${VERSION} -X agent/internal/app.BuiltAt=${BUILT_AT}" \
      -o /out/agent ./cmd/agent
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

# glab, for the same reason and from its own release. GitLab publishes both
# digests in the release's checksums file; bump the version and both together.
ARG GLAB_VERSION=1.118.0
ARG GLAB_SHA256_amd64=f3782ddb62b6ab20d0031699ea7b43f345dc6f63e991883660e21343b9524931
ARG GLAB_SHA256_arm64=0f6171766dd7f8246b7ce85bab18bd99d57ce2538e0ec9121c48fe004308eac4
RUN set -eu; \
    tarball="glab_${GLAB_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    case "$TARGETARCH" in \
      amd64) sha="$GLAB_SHA256_amd64" ;; \
      arm64) sha="$GLAB_SHA256_arm64" ;; \
      *) echo "no pinned digest for $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSLo "$tarball" \
      "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/${tarball}"; \
    echo "${sha}  ${tarball}" | sha256sum -c -; \
    tar -xzf "$tarball" --strip-components=1 -C /out bin/glab

FROM debian:bookworm-slim
# git for the clone and the push. ca-certificates is what makes every outbound
# call verifiable, the model gateway included. The sandbox adds nothing to this
# list — Landlock is a kernel facility the agent asks for directly, which is why
# it works in an unprivileged container where bubblewrap did not.
#
# chromium is the browser web_browse and web_search drive, and installing it here
# is what lets them stop searching for one: /usr/bin/chromium is a path the
# sandbox already grants, the same package CI installs, and the only one either
# tool will look at. It roughly doubles the image, which is the price of the
# tools working the same way everywhere they run.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git chromium \
      curl wget python3 perl \
 && rm -rf /var/lib/apt/lists/*

# The agent runs as one user, and it is not root. The data and workspace
# directories are created here so they are owned by that user before anything
# mounts over them.
RUN useradd --create-home --uid 10001 agent
WORKDIR /app
COPY --from=build /out/agent /app/agent
COPY --from=build /out/gh /usr/local/bin/gh
COPY --from=build /out/glab /usr/local/bin/glab
COPY --from=build /out/tools /app/tools
COPY web /app/web
COPY skills /app/skills
# What changed, as written by whoever changed it. It is read from disk at the
# version screen, because there is no repository here to derive it from.
COPY CHANGELOG.md /app/CHANGELOG.md
# Everything that outlives the container is under /app/state, which is the one
# volume a deployment mounts. The split inside it is for the agent's own use —
# transcripts and the database on one side, session working directories on the
# other — and is not a boundary: Landlock is, and it holds whether or not the
# two share a mount. Both are created here so a fresh named volume inherits an
# owner before anything runs.
RUN mkdir -p /app/state/data /app/state/workspace && chown -R agent:agent /app

USER agent
# Two roots and nothing else. /app/state is what outlives the container and is
# the one volume a deployment mounts; /app is what this image ships. Everything
# else — data, workspace, tools, skills, the interface, the changelog — is
# derived from one of the two, so a layout decision is made once here rather
# than restated on seven lines that have to agree.
ENV AGENT_ADDR=:7770 \
    AGENT_STATE=/app/state \
    AGENT_HOME=/app
EXPOSE 7770
# The agent asks itself, over /status, which answers without reaching a model,
# so the health check depends on nothing but the agent.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD ["/app/agent", "-health"]
ENTRYPOINT ["/app/agent"]
