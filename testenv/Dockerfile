# The environment the test suite and the evals run in, and nothing else. There
# is no source here: `task check` copies the working tree in when it runs, so
# this is built once and a change to the code never rebuilds it.
#
# It is CI's environment written down (.github/workflows/check.yml), and a
# change to one belongs in the other: Debian bookworm, the Go the image builds
# with, the Debian chromium package the runtime image installs, node 22 for
# web/*.test.js, and Task. Off Linux the kernel is the container VM's, which is
# Linux with Landlock, so this is where the whole suite runs — the agent does not
# build on the host at all (ADR-056).
FROM docker.io/library/golang:1.25-bookworm

RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium curl wget python3 perl \
      poppler-utils tesseract-ocr tesseract-ocr-eng tesseract-ocr-deu \
      python3-pip python3-venv python3-numpy python3-pil \
      unzip jq \
 && rm -rf /var/lib/apt/lists/*

# gh and glab, which the runtime image fetches from their own releases rather
# than from apt. The userland test proves the shell can run what the image
# ships, and it can only prove that where they are. The versions are the ones
# ../Dockerfile pins and move with them; the digests are checked there, where
# the binary is shipped rather than only tested against.
ARG GH_VERSION=2.100.0
ARG GLAB_VERSION=1.118.0
RUN set -eu; \
    arch="$(dpkg --print-architecture)"; \
    curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${arch}.tar.gz" \
      | tar -xz --strip-components=2 -C /usr/local/bin "gh_${GH_VERSION}_linux_${arch}/bin/gh"; \
    curl -fsSL "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/glab_${GLAB_VERSION}_linux_${arch}.tar.gz" \
      | tar -xz --strip-components=1 -C /usr/local/bin bin/glab

COPY --from=docker.io/library/node:22-bookworm-slim /usr/local/bin/node /usr/local/bin/node

ARG TASK_VERSION=v3.48.0
RUN go install github.com/go-task/task/v3/cmd/task@${TASK_VERSION}

# This machine claims to prove every test, so a test that cannot run here fails
# rather than skipping. A green run in this container means nothing was left out.
ENV AGENT_TEST_FULL=1

WORKDIR /src
