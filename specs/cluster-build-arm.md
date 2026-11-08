---
status: draft
---

## Summary

- Add a second build path to this repo: alongside today's local `docker build`, `make build BUILD_TARGET=sun` builds the image inside the sun cluster as a Kubernetes Job and pushes it to Docker Hub.
- Copy five `hack/` files: the runner, the Job template and the cluster alias map from `agent-task-executor` (its Job carries `automountServiceAccountToken: false`), plus the bootstrap script and its template from `go-skeleton`.
- Wrap the two existing `build` prerequisites in a `local` branch: `check-version-tag` refuses a scratch tag, and `check-go-mod` vendors into the local working tree — neither is meaningful for a build that runs from a clone inside a cluster.
- Switch the Dockerfile's build step from `-mod=vendor` to `-mod=mod`, because the cluster Job clones the repo with no `vendor/` directory and `vendor/` is gitignored.
- Keep the local arm's command, tag and guard exactly as they are, and keep `upload`, `clean` and `buca` local-only.

## Problem

`github-update-go-agent` is a publish-only source repo: `Makefile.docker` runs one `docker build` on the invoking machine, `upload` pushes it, and `buca` chains both. The image is consumed by the fleet — `bborbe/nuke`'s `agent/values-dev.yaml:1145` and `agent/values-prod.yaml:908` both set `image: bborbe/github-update-go-agent` with a mirrored semver tag — so every release of this repo currently needs a human with a working Docker daemon at the keyboard.

Four properties of this repo make a naive paste of the sibling pattern fail at runtime rather than at review:

- **`build` is chained to a version guard.** `Makefile.docker` declares `build: check-version-tag check-go-mod`, and `check-version-tag` refuses unless `git describe --tags --exact-match HEAD` equals `$(VERSION)`. A cluster build of a named commit with a scratch tag therefore exits 1 before any Job is created. The escape hatch that exists today (`ALLOW_UNTAGGED_BUILD=1`) is per-invocation and does not change the guard's placement.
- **`build` is also chained to `go mod vendor`.** `check-go-mod` writes `vendor/` into the invoking working tree. A cluster build builds a *named ref*, not the local `HEAD`, so preparing the local tree there is both meaningless and wrong.
- **`VERSION` has a default, and it is assigned after this file is parsed.** `Makefile` includes `Makefile.docker` at line 3 and only then assigns `VERSION := $(shell git describe --tags $(git rev-list --tags --max-count=1))` at lines 7–9. Inside `Makefile.docker`, `$(origin VERSION)` is therefore `undefined`, not `file`. A cluster arm that reads `$(VERSION)` unchanged would silently push the *latest release tag* for a build the caller never named — replacing a released image — and a parse-time guard written where the arm lives would never fire.
- **The Dockerfile builds from `vendor/`.** `RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -mod=vendor …`, while `.gitignore` excludes `/vendor`. A Job that clones the repo has no `vendor/` directory, so the image build fails before it starts.

Two further properties only matter once the arm exists:

- The local arm tags `$(DOCKER_REGISTRY)/$(IMAGE):$(VERSION)` where `DOCKER_REGISTRY ?= docker.io` and `IMAGE ?= bborbe/github-update-go-agent` — so the local arm already publishes to Docker Hub. The cluster push credential authenticates against Docker Hub too, so the two arms compose the *same* repository and need no separate cluster image variable.
- `upload`, `clean` and `buca` all assume a local Docker daemon. Left unguarded, `make buca BUILD_TARGET=sun` builds successfully in the cluster and then fails at the local push.

## Goal

`make build` on this repo produces the image locally exactly as it does today, and `make build BUILD_TARGET=sun REF=<sha> VERSION=<tag>` produces it inside the sun cluster as a Kubernetes Job pushing `docker.io/bborbe/github-update-go-agent:<tag>` — the repository the deployment consumes through nuke's mirror step. Both arms work from the same checkout, the cluster arm works from a fresh clone with no local preparation, and a cluster build can never be made to publish a tag the caller did not name.

## Non-goals

- Changing the local arm's command, tag or `check-version-tag` refusal. All stay as they are; the guard is re-scoped, never removed. The one shared change the local arm does inherit is the Dockerfile's move to module resolution, because both arms build the same Dockerfile.
- Making `sun` the Makefile default. `BUILD_TARGET ?= local` stays, so a bare `make build` is a local build.
- Renaming the image. `IMAGE ?= bborbe/github-update-go-agent` is what `bborbe/nuke` pulls.
- The nuke `MIRROR_IMAGES` entry for each new tag. The mirror copies the Docker Hub image into the private registry at pin-bump time; it is not produced by this build.
- Building at release or tag time. This spec adds the capability; *when* a build is triggered is a separate concern.
- Extracting the shared runner into an upstream template. The files are copied, not shared.
- Multi-arch images. `linux/amd64` only, matching the cluster nodes.
- Extra build knobs beyond `BUILD_TARGET`, `REF` and `VERSION`. The repository URL, the Dockerfile path and the push-secret name are literals in the dispatch.
- A CI / GitHub Actions build, or running `docker build` on sun over SSH. Both would need a new credential path and a new trigger; the fleet already has a Job-based build lane whose push credential is scoped to exactly this job, so the arm reuses it instead.

## Acceptance Criteria

- [ ] **The version guard still refuses on an untagged `HEAD`** — `make check-version-tag` exits non-zero and prints `ERROR: refusing to build` naming the tag mismatch, and `ALLOW_UNTAGGED_BUILD=1 make check-version-tag` exits 0. Evidence: both exit codes and the refusal text. This half runs in-container.
- [ ] **Post-Deploy (Rung-2):** the local arm still builds and tags as today, on the post-change tree — on this branch with no `BUILD_TARGET` set, `ALLOW_UNTAGGED_BUILD=1 make build VERSION=probe` exits 0 and `docker image inspect docker.io/bborbe/github-update-go-agent:probe` exits 0. Evidence: the build exit code and the inspected `.Id`. ⚠️ Run it on the branch, not on `v0.21.13`: that tag predates the change, so a build there exercises neither the new prerequisite list nor `-mod=mod`, and the regression gate would be inert.
  - `deploy_check:` `docker version --format '{{.Server.Version}}'`
  - `deploy_target:` `local-docker-daemon`
- [ ] **Post-Deploy (Rung-2):** the cluster arm builds and pushes from a fresh clone — `make build BUILD_TARGET=sun REF=<full 40-character sha> VERSION=<scratch-tag>` exits 0, and `crane digest docker.io/bborbe/github-update-go-agent:<scratch-tag>` resolves to a digest. Evidence: the exit code, the Job name, the build pod's `spec.nodeName`, the resolved digest, and `docker image inspect` on the same reference failing on this Mac with `No such image` — a stopped daemon or a malformed reference does not satisfy this.
  - `deploy_check:` `kubectlsun -n build get secret buildkit-push -o jsonpath='{.metadata.name}'`
  - `deploy_target:` `sun-cluster/build-namespace`
- [ ] **The cluster arm pushes the repository the deployment consumes** — `make -n build BUILD_TARGET=sun REF=<sha> VERSION=probe` prints the dispatch line carrying `IMAGE='docker.io/bborbe/github-update-go-agent'` and `TAG='probe'`. Evidence: the printed line, and the digest above resolving at exactly that repository.
- [ ] **`hack/` holds the five files and the cluster branch invokes the runner** — at the recorded head SHA, `git ls-tree --name-only HEAD hack/` lists `build-cluster.sh`, `cluster-alias.sh`, `build-job.yaml`, `build-cluster-bootstrap.sh` and `dockerconfig.json.tpl`; `grep -c '^BUILD_TARGET' Makefile.docker` is ≥ 1, `grep -cE '^[[:space:]]*hack/build-cluster\.sh' Makefile.docker` is ≥ 1, and `git ls-tree HEAD hack/ | grep -c '^100755'` is 3 — the three scripts are invoked as `hack/<script>` and lose the exec bit in a plain `cp`. Evidence: the five names, the mode count and both counts. ⚠️ The copied header comments name `BUILD_TARGET`, so a bare `grep -c BUILD_TARGET` passes on a comment alone — the `^` anchor is the probe.
- [ ] **The version guard is off the cluster arm's prerequisite list and still on the local one** — on an untagged `HEAD`, `make -p -n build BUILD_TARGET=sun REF=<sha> VERSION=probe 2>/dev/null | grep -E '^build:'` prints `build:` with no `check-version-tag`, while the same probe without `BUILD_TARGET` prints `build: check-version-tag check-go-mod`. Evidence: the two `build:` lines and the refusal from the first criterion. ⚠️ `make -n` alone cannot show this — it never executes a recipe, so an unwrapped and a wrapped guard both exit 0 under it.
- [ ] **The cluster arm refuses a tag it did not receive from the caller** — `make -n build BUILD_TARGET=sun REF=<sha>` with no `VERSION` on the command line exits non-zero and prints a message naming `VERSION`, and the same invocation with `VERSION=probe` exits 0. Evidence: both exit codes and the message. ⚠️ The guard must be evaluated where `VERSION`'s default is already assigned — `$(origin VERSION)` is `undefined` inside `Makefile.docker` at parse time — and must reject `undefined` as well as `file`, accepting only `command line` / `environment`.
- [ ] **`upload`, `clean` and `buca` stay local and are refused for a cluster target at parse time** — `make -n upload BUILD_TARGET=sun` exits non-zero naming the target as local-only, and so do `clean` and `buca`; the same three with no `BUILD_TARGET` print their `docker` recipes. Evidence: six exit codes and the refusal message. The refusal is a `$(error …)` so it fires under `-n` where no recipe runs.
- [ ] **The image builds from a clone with no `vendor/`** — the Dockerfile no longer builds from `vendor/`, the directory is still gitignored, and it is excluded from the build context. Evidence: `grep -c -- '-mod=vendor' Dockerfile` = 0, `grep -c -- '-mod=mod' Dockerfile` ≥ 1, `grep -c '^/vendor$' .gitignore` = 1, `grep -c '^vendor$' .dockerignore` = 1.
- [ ] **The documented build path matches reality** — `README.md` carries a *Build* section showing both invocations on one line each, stating that `buca` is local-only and that `VERSION` is required on the cluster arm, and pointing at `docs/design.md` for the image story; `.gitignore` gains the `specs` lock pattern; `CHANGELOG.md` carries a `## Unreleased` section with one bullet for this change. Evidence: `grep -c '^## Build' README.md` = 1, `grep -c 'make build BUILD_TARGET=sun REF=<sha> VERSION=<tag>' README.md` ≥ 1, `grep -c 'make build' README.md` ≥ 2, `grep -ciE 'buca.*local' README.md` ≥ 1, `awk '/^## Build/{f=1} f&&/design\.md/{c++} END{print c+0}' README.md` = 1, `grep -c '^/specs/in-progress/\*\.lock$' .gitignore` = 1, and `awk '/^## Unreleased/{f=1;next} /^## /{f=0} f&&/^- /{c++} END{print c+0}' CHANGELOG.md` = 1. ⚠️ `grep -c 'design.md' README.md` alone is already satisfied pre-change by the header's "Full design" link, so the cross-link must be scoped to the new section; and the CHANGELOG heading alone passes on an empty section.

## Verification

### Container-executable (runs inside the build container at prompt time)

- `make precommit` — lint, format and vet clean
- `make test` — unit and integration suites pass
- `bash -n hack/build-cluster.sh && bash -n hack/cluster-alias.sh && bash -n hack/build-cluster-bootstrap.sh` — the copied scripts parse
- `grep -c '^BUILD_TARGET' Makefile.docker` returns ≥ 1
- `grep -cE '^[[:space:]]*hack/build-cluster\.sh' Makefile.docker` returns ≥ 1
- `git ls-tree --name-only HEAD hack/` lists the five expected files
- `make check-version-tag` on an untagged HEAD exits non-zero; `ALLOW_UNTAGGED_BUILD=1 make check-version-tag` exits 0
- `make -p -n build 2>/dev/null | grep -E '^build:'` lists `check-version-tag check-go-mod`
- `make -p -n build BUILD_TARGET=sun REF=<sha> VERSION=probe 2>/dev/null | grep -E '^build:'` lists neither
- `make -n build BUILD_TARGET=sun REF=<sha> VERSION=probe` prints `hack/build-cluster.sh` and `IMAGE='docker.io/bborbe/github-update-go-agent'`
- `make -n build BUILD_TARGET=sun REF=<sha>` exits non-zero naming `VERSION`
- `make -n upload BUILD_TARGET=sun`, `make -n clean BUILD_TARGET=sun`, `make -n buca BUILD_TARGET=sun` each exit non-zero
- `grep -c -- '-mod=vendor' Dockerfile` returns 0; `grep -c -- '-mod=mod' Dockerfile` returns ≥ 1

### Operator-executable (runs on the host after PR merge)

- `make build BUILD_TARGET=sun REF=<sha> VERSION=<scratch-tag>` — one real cluster build; Job name, pod node and digest recorded
- `crane digest docker.io/bborbe/github-update-go-agent:<scratch-tag>` — the push landed
- `docker image inspect docker.io/bborbe/github-update-go-agent:<scratch-tag>` — fails on this Mac with `No such image`
- from the tagged tree: `make build` then `docker image inspect docker.io/bborbe/github-update-go-agent:v0.21.13` — the local arm still works

## Desired Behavior

1. **A build target selects the arm.** `BUILD_TARGET` defaults to `local`. With `local`, `make build` behaves exactly as today: it runs `check-version-tag` and `check-go-mod`, then `docker build`, tagging `$(DOCKER_REGISTRY)/$(IMAGE):$(VERSION)`.
2. **Any other value routes to a cluster Job.** With `BUILD_TARGET` set to a cluster alias or kubectl context, `make build` renders `hack/build-job.yaml` and applies it through `hack/build-cluster.sh`, passing the repository URL, the Dockerfile path and the push-secret name as literals, and `IMAGE='$(DOCKER_REGISTRY)/$(IMAGE)'` and `TAG='$(VERSION)'` from the make variables. It waits for the Job and reports the outcome with the Job's own log lines.
3. **The cluster arm needs a caller-supplied tag.** When `VERSION` came from the Makefile rather than from the caller, the cluster arm refuses before any Job is created, naming `VERSION`. A tag nobody chose can never be pushed over a release.
4. **Both arms publish to one repository.** `$(DOCKER_REGISTRY)/$(IMAGE)` composes `docker.io/bborbe/github-update-go-agent` under the current defaults, so the two arms cannot drift and no cluster-specific image variable is introduced.
5. **The guards are local-only.** `check-version-tag` and `check-go-mod` remain prerequisites of `build` for `local` and are compiled out for any other target. `upload`, `clean` and `buca` are local-only and are refused at parse time for a cluster target.
6. **The cluster build works from a clone.** The image build resolves its Go dependencies in module mode, so a Job that clones the repo with no `vendor/` directory builds the same way a developer's checkout does.
7. **The documented build path matches reality.** The README shows both arms and states the `VERSION` requirement and the `buca` limitation; the CHANGELOG records the change.

## Constraints

- The local arm's command, tag and guard must not change. A bare `make build` on an untagged `HEAD` must still fail — that refusal is what keeps a released tag from being overwritten by a branch build.
- `check-go-mod` stays on the local branch only. The cluster arm builds a named ref, not the local `HEAD`, so vendoring into the local tree there is meaningless.
- **The `VERSION` guard must be evaluated after `Makefile`'s default assignment.** `Makefile` includes `Makefile.docker` at line 3 and assigns the default at lines 7–9, so `$(origin VERSION)` inside `Makefile.docker` is `undefined` at parse time. Put the check in `Makefile` after the default, or at recipe-expansion time in the cluster branch (a `$(error …)` inside a recipe fires under `make -n`; a parse-time test there does not). Reject `undefined` as well as `file`; accept only `command line` and `environment`. The check is keyed on `BUILD_TARGET` being non-`local`, so a bare local build is unaffected — a check that fires for the local arm too breaks the behaviour the first criterion protects.
- The local-only refusal for `upload` / `clean` / `buca` is a `$(error …)` keyed on `BUILD_TARGET`, not a recipe-level `exit 1`. A recipe-level refusal is invisible to `make -n`, so the criterion could not observe it.
- The three shared `hack/` files are copied unmodified at copy time from `agent-task-executor` at `origin/master`: `hack/build-cluster.sh`, `hack/cluster-alias.sh`, `hack/build-job.yaml`. Any later deviation is a deliberate, documented edit, not drift. That repository is public, so a `git clone --depth=1 https://github.com/bborbe/agent-task-executor` reaches them. `cluster-alias.sh` is required — the runner sources it and fails at that line without it.
- `hack/build-cluster-bootstrap.sh` and `hack/dockerconfig.json.tpl` are copied unmodified at copy time from `go-skeleton` at `origin/master`; it is the only repo carrying them. They are not called by the arm: they are the reproducible provisioning path for the `build` namespace and the push secret, so a cluster whose prerequisites are missing can be rebuilt from the repo rather than from memory. The template holds a TeamVault *lookup key*, not a credential.
- **File modes are preserved.** `build-cluster.sh`, `cluster-alias.sh` and `build-cluster-bootstrap.sh` are executable upstream (`100755`) and are invoked as `hack/build-cluster.sh`; a copy that loses the bit makes the dispatch fail at exec.
- The Job template already passes `DOCKER_REGISTRY=docker.io` as a build argument, so the Dockerfile's base images resolve from the public registry inside the cluster rather than from the private mirror. Do not change that back to the mirror.
- **The module-mode change affects the local arm too.** Under `-mod=mod` Go ignores `vendor/`, so the local build resolves modules over the network instead of from the vendored tree. Add `vendor` to `.dockerignore` so the local build context stops shipping a dead `vendor/`.
- `IMAGE`, the registry and the tag must each satisfy the runner's character whitelist; no value may carry a newline.
- `.gitignore` keeps every entry it has today, including `/vendor`, and gains `/specs/in-progress/*.lock`. The consumer is dark-factory itself: it writes `specs/in-progress/<id>-<name>.md.lock` while a spec is in progress, and `/prompts/**/*.md.lock` covers the prompt lane with no `specs` equivalent — so without this line the first approved spec leaves an untracked lock file in the tree.
- `README.md` is the in-repo home for the two-arm topology and the nuke coupling; the spec dies after implementation, the README does not. `docs/design.md` documents the image and its deployment story, so it gains one sentence pointing at the README's build section.
- No new scenario. The behaviour is reachable by checks on the rendered invocation plus one real cluster build.

## Assumptions

These must hold before the cluster criterion can pass; none is created by this spec.

- The `build` namespace and the `buildkit-push` dockerconfigjson secret exist in the sun cluster, and that credential covers Docker Hub — the registry both arms push to under the current defaults.
- The repo is public, so the Job's anonymous `git fetch` succeeds; the init container carries no clone credential.
- `crane` is installed on the host running the criteria.
- `REF` exists in the local repository **and on `origin`**: the runner derives `SOURCE_DATE_EPOCH` from the local repository, and the Job's init container fetches the ref from the remote. A commit that exists only locally fails at the clone.
- The build pod can reach the Go module proxy. This is new load on the cluster arm: the local arm vendored first, the cluster arm resolves modules at build time.
- `kubectl` is configured for the target cluster through the alias files `hack/cluster-alias.sh` resolves.

## Failure Modes

| Trigger | Expected behavior | Recovery | Reversibility |
|---|---|---|---|
| `BUILD_TARGET` names an unknown cluster or context | The runner fails before applying anything and lists the accepted values | Re-run with a listed target | Nothing created |
| `VERSION` omitted on the cluster arm | Refusal naming `VERSION`, before the Job is rendered | Re-run passing `VERSION=<scratch-tag>` | Nothing created |
| `REF` is not in the local repository | The runner dies resolving `SOURCE_DATE_EPOCH` | `git fetch`, then re-run with the full 40-character SHA | Nothing created |
| `REF` is a short SHA, or exists locally but was never pushed | The clone init container fails: `git fetch --depth=1 origin <short>` cannot find the ref | Push the branch or commit, then re-run with the full SHA | Nothing created |
| The clone's commit epoch differs from the locally derived one | The build container refuses before `buildctl` runs and names both epochs | `git fetch`, then re-run | Nothing created |
| A Job with the derived name already exists in `build` | The runner refuses and names the existing Job rather than overwriting it | Delete the finished Job, then re-run | Nothing overwritten |
| The runner is interrupted after `apply` | The Job keeps running in the cluster while the local invocation exits; the next run then hits the existing-Job refusal | Inspect `kubectl -n build get jobs`, read the pod's log, delete the Job once finished | Reversible: delete the Job |
| The build pod cannot reach the module proxy | The Job fails; the runner prints the last 100 log lines and the Job name | Inspect the Job logs; the local arm is unaffected | Reversible: delete the Job |
| The build pod is OOM-killed or throttled | The Job reports `failed`; the pod status carries `OOMKilled` or a CPU-throttling event | Raise the template's limits in the same change, or build locally for that one image | Reversible: delete the Job |
| The registry throttles or rejects the push | The build fails at the push step with the registry's own error | `kubectlsun -n build get secret buildkit-push -o jsonpath='{.metadata.name}'`, then re-run | Nothing pushed |
| A caller-supplied `VERSION` names an already-released tag | No guard — the push replaces that tag irreversibly | Re-push the image from the released tag, then cut a new release | **Irreversible**: the overwritten tag is gone |
| The local arm regresses | `make build` from the tagged tree fails or tags differently | Revert the arm; the local path is the regression gate | Reversible: revert the commit |

## Security / Abuse Cases

- The runner substitutes caller-supplied values into a manifest and into embedded shell, so every value is checked against a character whitelist and rejected for embedded newlines before rendering. Do not add a value path that bypasses that check.
- The repository URL, the Dockerfile path and the push-secret name are literals in the dispatch, not configurable inputs. A configurable clone source on a build that mounts a push credential would let a caller point the credential-bearing Job at another repo.
- The build Job mounts a push credential from a `build`-namespace secret and runs with `automountServiceAccountToken: false`. The credential is scoped to pushing an image; it grants nothing else.
- The build container runs as an unprivileged user with the sandbox profiles relaxed because rootless BuildKit requires it. That relaxation is deliberate and must not be copied to the clone init container.
- `VERSION` is caller-controlled and becomes an image tag and a Job name. The tag is whitelisted and the Job name is sanitized to a valid DNS name, truncated, and stripped of trailing separators.
- The bootstrap script reads a credential from TeamVault into a `600` temp file for the length of one `create secret --dry-run` and removes it on every exit path. It is never echoed and never written into the repo.

## Suggested Decomposition

Prompts should be generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | `Makefile.docker` dispatch: the `BUILD_TARGET` split, the deferred `VERSION` refusal, local-only `upload`/`clean`/`buca`, and the guards compiled out for a cluster target | 1, 2, 3, 4, 5 | 1, 4, 6, 7, 8 | — |
| 2 | The build path itself: the five `hack/` files, the module-mode Dockerfile change, `.dockerignore` | 2, 6 | 2, 3, 5, 9 | prompt 1 (the dispatch calls the runner) |
| 3 | Docs: the README build section, the `docs/design.md` cross-link and the CHANGELOG bullet | 7 | 10 | — |

Rationale: prompt 1 establishes the dispatch and the guard scoping that make any of it reachable; prompt 2 supplies the runner the dispatch calls and the Dockerfile change it needs; prompt 3 is independent and touches no build logic. The spec is above the 50-point size heuristic; the three-prompt split is what keeps each unit bounded, and a completed reference implementation bounds prompt-creator research.

## Do-Nothing Option

Leaving this repo without a cluster arm keeps its image tied to the Mac. Every release cut while the operator is away from this machine, and every future unattended pipeline, needs a human with a Docker daemon for this one repo — while its siblings build themselves. The cost is not the one build; it is that the fleet's release path has a hole shaped exactly like the repo nobody is watching.
