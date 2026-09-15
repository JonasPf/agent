# The environment the test suite and the evals run in, and nothing else. There
# is no source here: `task check:container` copies the working tree in when it
# runs, so this is built once and a change to the code never rebuilds it.
#
# It is CI's environment written down (.github/workflows/check.yml), and a
# change to one belongs in the other: Debian bookworm, the Go the image builds
# with, the Debian chromium package the runtime image installs, node 22 for
# web/*.test.js, and Task. On a Mac the kernel is the container VM's, which is
# Linux with Landlock, so the browser and sandbox tests that skip on the host
# run here.
FROM docker.io/library/golang:1.25-bookworm

RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium curl wget python3 perl \
 && rm -rf /var/lib/apt/lists/*

COPY --from=docker.io/library/node:22-bookworm-slim /usr/local/bin/node /usr/local/bin/node

ARG TASK_VERSION=v3.48.0
RUN go install github.com/go-task/task/v3/cmd/task@${TASK_VERSION}

# This machine claims to prove every test, so a test that cannot run here fails
# rather than skipping. A green run in this container means nothing was left out.
ENV AGENT_TEST_FULL=1

WORKDIR /src
