---
status: completed
spec: [009-cluster-build-arm]
summary: Replaced Makefile.docker with a BUILD_TARGET split (local keeps check-version-tag/check-go-mod and the unchanged docker build; cluster dispatches to hack/build-cluster.sh with a caller-only VERSION guard and local-only upload/clean/buca refusals) and added the Unreleased CHANGELOG bullet; make precommit exits 0 (run with the container-injected GIT_AUTHOR_*/GIT_COMMITTER_* vars unset, since they override the code's -c user.name and are unrelated to this change).
execution_id: ca50dcd1-c9a2-5752-b6d6-83d7e195168b-exec-021-spec-009-cluster-build-dispatch
dark-factory-version: dev
created: "2026-11-08T23:32:54Z"
queued: "2026-11-08T23:52:59Z"
started: "2026-11-08T23:53:00Z"
completed: "2026-11-09T00:25:13Z"
branch: dark-factory/cluster-build-arm
---

<summary>
- A new build switch selects where the image is built: the local Docker daemon (the default, unchanged) or a named cluster.
- A cluster build runs as a Kubernetes Job and pushes the image to Docker Hub.
- The local build keeps its existing version-tag refusal and its vendoring step exactly as they are.
- The cluster build does not run those two local-tree guards at all.
- The cluster build refuses to start unless the caller supplies the image tag explicitly, so it can never overwrite a released tag by accident.
- The upload, clean and build-upload-clean chain is refused for a cluster target with a message saying it is local-only.
- The local build command, tag and refusal behavior are unchanged.
- No new configuration knobs are introduced: the cluster target, the ref and the tag are the only inputs.
</summary>

<objective>
Split `make build` into two arms behind `BUILD_TARGET`. With `BUILD_TARGET=local` (the default), the existing local `docker build` recipe runs with its `check-version-tag` and `check-go-mod` prerequisites unchanged. With any other value, the local-tree guards are compiled out and the build is dispatched to `hack/build-cluster.sh`, which renders and applies the cluster Job; the cluster arm refuses before rendering unless `VERSION` came from the caller (`command line` or `environment`), and `upload`, `clean` and `buca` are refused as local-only under `make -n` as well as during a real run.
</objective>

<context>
Read the repo-root `Makefile`, `Makefile.docker`, `Makefile.variables` and `Makefile.precommit` before editing. The include order in `Makefile` matters: `include Makefile.docker` is line 3 and the `VERSION` default is assigned at lines 7-9, so `$(origin VERSION)` is `undefined` while `Makefile.docker` is parsed and becomes `file` only after the whole `Makefile` has been read. That is why the cluster-arm check in this prompt lives in a recipe (expanded after parsing), not at parse time.

Read the approved spec `specs/in-progress/009-cluster-build-arm.md`: Goal, Desired Behavior 1-5, Constraints, Acceptance Criteria 1, 4, 6, 7 and 8, and Failure Modes rows 1 and 2.

Read the reference implementation at `https://github.com/bborbe/agent-task-executor` (`Makefile.docker` on `origin/master`). It is a reference only. Do NOT copy these parts of it:
- `BUILD_TIMEOUT`, `BUILD_REPO_URL`, `BUILD_DOCKERFILE`, `BUILD_PUSH_SECRET` and `ALLOW_DEFAULT_VERSION` — the spec's Non-goals forbid extra build knobs, and `ALLOW_DEFAULT_VERSION` is an escape hatch on the very guard this spec ships.
- its unconditional `build: check-version-tag` prerequisite — the spec requires the prerequisite list itself to differ (`build: check-version-tag check-go-mod` for local, `build:` for cluster), which `make -p -n` must show.
- its `upload`/`clean` "nothing to do" no-op bodies — the spec requires a `$(error …)` refusal that is visible under `make -n`.

Read `/home/node/.claude/plugins/marketplaces/coding/docs/go-makefile-commands.md` for the repo's make conventions.

Important environment note: this dark-factory run has `hideGit` effective, so the execution container masks the repo's `.git`. Do not rely on the repo's `.git` in the verification section. The `make -p -n` / `make -n` probes below are safe because they never execute a recipe; the `check-version-tag` probe uses a stub `git` on `PATH` so the real recipe runs without the repo's `.git`. The spec's git-dependent evidence (`git ls-tree`, a real tagged build) is operator-side and is not repeated here.

Run this first to see the current structure:

```bash
sed -n '1,80p' Makefile.docker
sed -n '1,15p' Makefile
grep -n '^include' Makefile
```
</context>

<requirements>
1. Replace the entire contents of `Makefile.docker` with the file below. Every recipe line begins with a real TAB character (not spaces); `make` reports "missing separator" otherwise. The local `build`, `check-version-tag` and `check-go-mod` bodies are copied verbatim from today's file; only their placement under the `ifeq ($(BUILD_TARGET),local)` branch is new.

```make
DIRS += $(shell find */* -maxdepth 0 -name Makefile -exec dirname "{}" \;)

# BUILD_TARGET selects where `make build` runs. `local` (the default) uses the
# local Docker daemon; any other value names a cluster alias (sun / nukedev /
# nukeprod) or a kubectl context and builds inside that cluster as a Job
# rendered from hack/build-job.yaml by hack/build-cluster.sh.
BUILD_TARGET ?= local

ifeq ($(BUILD_TARGET),local)
.PHONY: build
build: check-version-tag check-go-mod
	@if [ -z "$(VERSION)" ]; then echo "ERROR: no VERSION set and no git tag found — set VERSION=vX.Y.Z or create a tag" >&2; exit 1; fi
	DOCKER_BUILDKIT=1 \
	docker build \
	--pull \
	--no-cache \
	--rm=true \
	--platform=linux/amd64 \
	--build-arg DOCKER_REGISTRY=$(DOCKER_REGISTRY) \
	--build-arg BUILD_GIT_VERSION=$$(git describe --tags --always --dirty) \
	--build-arg BUILD_GIT_COMMIT=$$(git rev-parse --short HEAD) \
	--build-arg BUILD_DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ) \
	-t $(DOCKER_REGISTRY)/$(IMAGE):$(VERSION) \
	-f Dockerfile .

.PHONY: check-version-tag
check-version-tag:
	@if [ -n "$(ALLOW_UNTAGGED_BUILD)" ]; then \
		echo "ALLOW_UNTAGGED_BUILD set — skipping version/tag check"; \
	else \
		head_tag=$$(git describe --tags --exact-match HEAD 2>/dev/null); \
		if [ "$$head_tag" != "$(VERSION)" ]; then \
			echo "ERROR: refusing to build $(VERSION) from this tree." >&2; \
			echo "  HEAD is at tag: $${head_tag:-<untagged>}" >&2; \
			echo "  building as:    $(VERSION)" >&2; \
			echo "  An image stamped vX.Y.Z must be built from the vX.Y.Z tag." >&2; \
			echo "  Fix: git checkout $(VERSION)   (or set ALLOW_UNTAGGED_BUILD=1 for a scratch build)" >&2; \
			exit 1; \
		fi; \
	fi

.PHONY: check-go-mod
check-go-mod:
	@if [ -f "go.mod" ]; then \
		echo "go.mod found, running go mod vendor..."; \
		go mod vendor; \
	else \
		echo "go.mod not found, skipping go mod vendor."; \
	fi
else
# The cluster arm builds a named REF in a fresh clone, so the local-tree guards
# (check-version-tag / check-go-mod) are compiled out above. VERSION must come
# from the caller: the default in Makefile is the newest release tag, and
# pushing that over a released image is irreversible. `$(origin VERSION)` is
# expanded when this recipe is expanded, after Makefile has assigned the
# default, so `file`/`undefined` are rejected and only `command line` /
# `environment` are accepted. The `$(error …)` fires under `make -n` too.
.PHONY: build
build:
	@$(if $(filter command line environment,$(origin VERSION)),,$(error the cluster arm needs an explicit VERSION; pass VERSION=<scratch-tag>))
	BUILD_TARGET='$(BUILD_TARGET)' \
	REPO_URL='https://github.com/bborbe/github-update-go-agent.git' \
	REF='$(REF)' \
	TAG='$(VERSION)' \
	DOCKERFILE='Dockerfile' \
	IMAGE='$(DOCKER_REGISTRY)/$(IMAGE)' \
	PUSH='true' \
	PUSH_SECRET='buildkit-push' \
	hack/build-cluster.sh
endif

.PHONY: apply
apply:
	@for i in $(DIRS); do \
		cd $$i; \
		echo "apply $${i}"; \
		make apply; \
		cd ..; \
	done

# upload, clean and buca assume a local Docker daemon. For a cluster target the
# image is pushed from inside the Job and nothing lands on this host, so they
# are refused rather than silently no-oping. The $(error …) is inside a recipe
# so it fires under `make -n`, which never executes a recipe.
ifeq ($(BUILD_TARGET),local)
.PHONY: upload
upload:
	docker push $(DOCKER_REGISTRY)/$(IMAGE):$(VERSION)

.PHONY: clean
clean:
	docker rmi $(DOCKER_REGISTRY)/$(IMAGE):$(VERSION) || true
	docker image prune -f || true
	docker builder prune --all -f || true
	rm -rf vendor

.PHONY: buca
buca: build upload clean apply
else
.PHONY: upload clean buca
upload clean buca:
	@$(error $@ is local-only; BUILD_TARGET=$(BUILD_TARGET) builds and pushes inside the cluster)
endif
```

2. Keep `Makefile` unchanged. The `VERSION` default at lines 7-9 must stay where it is: the cluster-arm guard relies on it being assigned before the recipe is expanded. Do not add a parse-time check to `Makefile`; a parse-time check would fire for `make -n upload BUILD_TARGET=sun` and replace the local-only refusal message with a VERSION message.

3. Keep the dispatch's literals literal. `REPO_URL='https://github.com/bborbe/github-update-go-agent.git'`, `DOCKERFILE='Dockerfile'` and `PUSH_SECRET='buildkit-push'` are not configurable inputs. Do not add `BUILD_REPO_URL`, `BUILD_DOCKERFILE`, `BUILD_PUSH_SECRET`, `BUILD_TIMEOUT`, `BUILD_NAMESPACE`, `ALLOW_DEFAULT_VERSION`, or any other variable. Do not pass `NAMESPACE`, `TIMEOUT` or `DOCKER_REGISTRY`: `hack/build-cluster.sh` defaults them (`NAMESPACE=build`, `TIMEOUT=3600`, `DOCKER_REGISTRY=docker.io`), and the Job template already hardcodes the public registry as its build argument.

4. The cluster-arm `VERSION` guard must reject every origin except `command line` and `environment`. In particular it must reject `file` (the Makefile default, which is the newest release tag) and `undefined`. The guard is a recipe-level `$(error …)`, so it fires under `make -n`; a shell `exit 1` would not, because `make -n` never runs the shell. The message must name `VERSION` and tell the caller to pass `VERSION=<scratch-tag>`.

5. The local-only refusal must use `$(error …)` inside the recipe bodies of `upload`, `clean` and `buca`, keyed on `BUILD_TARGET` being non-`local`. `buca` must not keep `build upload clean apply` as prerequisites in the cluster branch: otherwise `make -n buca BUILD_TARGET=sun` would expand the `build` recipe first and report the VERSION guard instead of naming `buca` as local-only. The local `buca: build upload clean apply` is unchanged.

6. Do not change `apply`; it stays available for every target (it is a no-op placeholder today).

7. Preserve the `DIRS += $(shell find …)` line at the top of the file.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT modify `Makefile`, `Makefile.variables`, `Makefile.precommit`, `Dockerfile`, `.dockerignore`, `README.md`, `CHANGELOG.md`, `docs/`, or any `.go` file in this prompt.
- The local arm's command, tag and `check-version-tag` refusal must not change. A bare `make build` on an untagged `HEAD` must still fail exactly as it does today.
- `check-go-mod` stays a prerequisite of the local `build` only. The cluster arm builds a named ref, not the local `HEAD`, so vendoring into the local tree there is meaningless and wrong.
- `BUILD_TARGET ?= local` is the only new variable. No cluster-specific image variable, no registry variable, no timeout variable, no escape hatch on the VERSION guard.
- The three scripts `hack/build-cluster.sh`, `hack/cluster-alias.sh` and `hack/build-cluster-bootstrap.sh` are supplied by the sibling prompt; this prompt must not create, edit or delete them. The dispatch line references `hack/build-cluster.sh` only.
- The repository URL, Dockerfile path and push-secret name are literals in the dispatch. A configurable clone source on a Job that mounts a push credential would let a caller point the credential at another repo (spec Security / Abuse Cases).
- No new scenario. The behavior is reachable by the `make -n` probes below plus the operator-side real cluster build in the spec.
</constraints>

<verification>
Run `make precommit` — must exit 0.

```bash
grep -c '^BUILD_TARGET' Makefile.docker
# expect: >= 1

grep -cE '^[[:space:]]*hack/build-cluster\.sh' Makefile.docker
# expect: >= 1
```

AC1 — the version guard still refuses, exercised through the real recipe without the repo's masked `.git`:

```bash
stub=$(mktemp -d)
mkdir -p "$stub/bin"
printf '%s\n' '#!/bin/sh' \
  'case "$*" in' \
  '  *"describe --tags --exact-match HEAD"*) exit 0 ;;' \
  '  *) echo "" ;;' \
  'esac' > "$stub/bin/git"
chmod +x "$stub/bin/git"

VERSION=v0.0.2 PATH="$stub/bin:$PATH" make check-version-tag
# expect: non-zero exit and stderr contains "ERROR: refusing to build"

ALLOW_UNTAGGED_BUILD=1 VERSION=v0.0.2 PATH="$stub/bin:$PATH" make check-version-tag
# expect: exit 0

rm -rf "$stub"
```

AC6 — the guard is off the cluster prerequisite list and still on the local one:

```bash
make -p -n build 2>/dev/null | grep -E '^build:'
# expect: build: check-version-tag check-go-mod

make -p -n build BUILD_TARGET=sun REF=abc VERSION=probe 2>/dev/null | grep -E '^build:'
# expect: build:
```

AC4 and Desired Behavior 2 — the dispatch prints the right repository and tag:

```bash
make -n build BUILD_TARGET=sun REF=abc VERSION=probe
# expect: exit 0; output contains
#   IMAGE='docker.io/bborbe/github-update-go-agent'
#   TAG='probe'
#   hack/build-cluster.sh
```

AC7 — a tag the caller did not name is refused:

```bash
make -n build BUILD_TARGET=sun REF=abc; echo "exit=$?"
# expect: non-zero; the message names VERSION
```

AC8 — `upload`, `clean` and `buca` are refused for a cluster target under `-n`:

```bash
make -n upload BUILD_TARGET=sun; echo "exit=$?"   # non-zero; names upload as local-only
make -n clean BUILD_TARGET=sun; echo "exit=$?"    # non-zero; names clean as local-only
make -n buca BUILD_TARGET=sun; echo "exit=$?"     # non-zero; names buca as local-only
```

Finally, confirm the local arm is unchanged by inspection:

```bash
sed -n '/^ifeq ($(BUILD_TARGET),local)/,/^else/p' Makefile.docker
# expect: build: check-version-tag check-go-mod, the unchanged docker build recipe,
#         and the unchanged check-version-tag / check-go-mod bodies
```
</verification>
