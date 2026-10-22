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
# Pre-warm the Go module + build cache for the nine scanners the repo gates
# invoke via `go run <tool>@<ver>` (the tools.env pins errcheck and gosec are
# omitted: Makefile.precommit embeds both in golangci-lint, so no gate runs
# them). On a cold pod each tool is downloaded and compiled per Job (minutes);
# with the cache baked in at the runtime HOME, a repo pinning the canonical
# version gets a cache hit.
# A repo pinning another version just misses the cache — today's behaviour.
# Versions are read from tools.env so there is no second copy of the pins.
# Cost (measured 2026-10-21, linux/amd64): +4.09 GB layer (2.4 GB modules,
# 1.5 GB build cache; image ~1.2 -> 5.3 GB), ~9 min build under emulation.
# NOT used in production: every fleet sets GOMODCACHE/GOCACHE over a PVC, so Go
# never reads this cache and no Job gets the saving. The layer is slated for
# removal — see the v0.21.1 CHANGELOG entry.
ENV HOME=/home/claude
COPY tools.env /tmp/tools.env
RUN set -e; v() { sed -nE "s/^$1[[:space:]]*\?=[[:space:]]*([^[:space:]]+).*/\1/p" /tmp/tools.env; }; \
    for t in \
      "github.com/shoenig/go-modtool@$(v GO_MODTOOL_VERSION)" \
      "github.com/incu6us/goimports-reviser/v3@$(v GOIMPORTS_REVISER_VERSION)" \
      "github.com/segmentio/golines@$(v GOLINES_VERSION)" \
      "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(v GOLANGCI_LINT_VERSION)" \
      "golang.org/x/vuln/cmd/govulncheck@$(v GOVULNCHECK_VERSION)" \
      "github.com/google/osv-scanner/v2/cmd/osv-scanner@$(v OSV_SCANNER_VERSION)" \
      "github.com/google/addlicense@$(v ADDLICENSE_VERSION)" \
      "github.com/maxbrunsfeld/counterfeiter/v6@$(v COUNTERFEITER_VERSION)" \
      "github.com/onsi/ginkgo/v2/ginkgo@$(v GINKGO_VERSION)" \
    ; do \
      case "$t" in *@) echo "missing version for $t" >&2; exit 1;; esac; \
      # go install fails the build on a download/compile error (set -e), so a
      # bad pin or network failure cannot ship a silently cold cache.
      GOBIN=/tmp/warm-bin go install "$t"; \
      # go run caches the linked executable too; --help exit codes vary per tool.
      go run "$t" --help >/dev/null 2>&1 </dev/null || true; \
    done; \
    # Smoke assertion: the caches must be non-empty and a re-run of one tool
    # must resolve from them, so a base-image or HOME drift that would silently
    # re-cold-start every gate fails the build instead.
    [ -n "$(ls -A "$(go env GOMODCACHE)")" ] && [ -n "$(ls -A "$(go env GOCACHE)")" ]; \
    case "$(go run github.com/google/addlicense@$(v ADDLICENSE_VERSION) --help 2>&1 </dev/null)" in \
      *"go: downloading"*) echo "warm cache not used: addlicense re-downloaded" >&2; exit 1;; \
    esac; \
    rm -rf /tmp/tools.env /tmp/warm-bin

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
