ARG DOCKER_REGISTRY=docker.quant.benjamin-borbe.de:443
FROM ${DOCKER_REGISTRY}/golang:1.26.5 AS build
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
RUN apk --no-cache add ca-certificates curl bash git github-cli make jq util-linux nodejs npm \
 && npm install -g --omit=dev --no-optional @anthropic-ai/claude-code \
 && npm cache clean --force \
 && apk del npm \
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
RUN mkdir -p /home/claude/.claude
ENV ZONEINFO=/zoneinfo.zip
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /
ENV BUILD_GIT_VERSION=${BUILD_GIT_VERSION}
ENV BUILD_GIT_COMMIT=${BUILD_GIT_COMMIT}
ENV BUILD_DATE=${BUILD_DATE}
ENTRYPOINT ["/main", "-v=2"]
