ARG DOCKER_REGISTRY=docker.prod.nuke.benjamin-borbe.de:443
FROM ${DOCKER_REGISTRY}/golang:1.27.3 AS build
ARG BUILD_GIT_VERSION=dev
ARG BUILD_GIT_COMMIT=none
ARG BUILD_DATE=unknown
COPY . /workspace
WORKDIR /workspace
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -mod=vendor -ldflags "-s" -a -installsuffix cgo -o /main
CMD ["/bin/bash"]

FROM ${DOCKER_REGISTRY}/alpine:3.23 AS alpine
# Runtime toolchain (design § 2.5 / § 7.3): the agent clones, updates, and
# gates real Go repos in-pod, so it needs git + make + gh + jq/column plus
# the full Go toolchain. Most scanners/linters run via `go run tool@version`
# from the repo's Makefile — only trivy must be a system binary.
# gcc + musl-dev: repo gates run `go test -race`, which requires cgo.
# npm is KEPT (not `apk del`'d after installing the Claude CLI): a repo whose
# `make precommit` recurses into a JS subproject needs it, and without it the
# gate dies with `npm: No such file or directory` (exit 127) at planning, so the
# task can never pass and retries forever — observed on bborbe/backup, 2026-08-19.
# Costs 7.5 MB; node is already here for the Claude CLI.
# X11 dev headers: repos depending on github.com/go-vgo/robotgo (a cgo GUI-automation
# library) cannot compile without them — `go test` dies at `X11/Xlib.h: No such file or
# directory`, so `make precommit` exits 2, the task reports failed, and the dispatcher
# re-runs it every ~5 min forever against the single job slot. Observed on bborbe/beactive,
# 2026-08-30: 4 identical failures in 18 minutes. Costs 48.7 MB (+3.2% of the image; 49 packages).
# tesseract/leptonica are deliberately NOT added: gosseract is an indirect dependency that
# never enters the compiled path, and including it would cost 92 MB for nothing.
# openssl: a target repo's gate may shell out to the `openssl` CLI, which this image
# does not carry. Observed on bborbe/agent-claude, 2026-10-14: its vendored
# scripts/git_credential_github_app_test.py:37 calls
# `subprocess.run(["openssl", "genrsa", "2048"])`, so `make precommit` died at the
# python-test target with `FileNotFoundError: [Errno 2] No such file or directory:
# 'openssl'` (exit 2) — and the dispatcher re-runs the task every ~5 min forever
# against the single job slot. python3/make/git/gh/jq/column/trivy were all already
# present, so openssl was the only binary missing for that gate.
RUN apk --no-cache add ca-certificates curl bash git github-cli make jq util-linux nodejs npm gcc musl-dev \
      libx11-dev libxtst-dev libxkbcommon-dev libxinerama-dev libxcursor-dev openssl \
 && npm install -g --omit=dev --no-optional @anthropic-ai/claude-code \
 && npm cache clean --force \
 && rm -rf /root/.npm /tmp/*
RUN curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh \
  | sh -s -- -b /usr/local/bin \
 && trivy --version
# Go toolchain copied from the build image — design D5: the image toolchain
# IS the go-directive bump target, kept current by this very pipeline.
COPY --from=build /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:${PATH}

FROM alpine
ARG BUILD_GIT_VERSION=dev
ARG BUILD_GIT_COMMIT=none
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.version="${BUILD_GIT_VERSION}"
COPY --from=build /main /main
COPY agent/ /agent/
ENV HOME=/home/claude
# Run as non-root: as root, a target repo's tests that expect a write to a
# read-only directory to fail pass on a developer machine and in CI but fail
# here (root bypasses directory permissions). Observed on bborbe/vault-cli,
# 2026-10-21: 6 "read-only directory" specs failed, ~55 update-go tasks aborted.
# Workdirs live under os.TempDir and the Go caches under $HOME, both writable by uid 1000.
RUN adduser -D -u 1000 -h /home/claude claude \
 && mkdir -p /home/claude/.claude \
 && chown -R 1000:1000 /home/claude
USER 1000:1000
ENV ZONEINFO=/zoneinfo.zip
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /
ENV BUILD_GIT_VERSION=${BUILD_GIT_VERSION}
ENV BUILD_GIT_COMMIT=${BUILD_GIT_COMMIT}
ENV BUILD_DATE=${BUILD_DATE}
ENTRYPOINT ["/main", "-v=2"]
