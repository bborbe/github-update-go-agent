---
status: approved
spec: [009-cluster-build-arm]
created: "2026-11-08T23:32:54Z"
queued: "2026-11-08T23:57:52Z"
branch: dark-factory/cluster-build-arm
---

<summary>
- The repository gains the five helper files the cluster build needs: the runner, the cluster alias map, the Job template, the bootstrap script and its credential template.
- The three helper scripts keep their executable bits so the build dispatch can invoke them directly.
- The image build resolves Go modules instead of using a vendored directory, so it works from a fresh clone that has no vendor folder.
- The local build context no longer ships a vendor directory.
- The build Job keeps its existing push-credential mount, its non-root build user, its relaxed sandbox profiles and its hardened clone container.
- The helper files are copied unmodified from their upstream repositories, not re-written.
</summary>

<objective>
Add the build path the dispatch from the sibling prompt calls: copy the five `hack/` files unmodified from their upstream repositories with their modes intact, switch the Dockerfile's Go build from `-mod=vendor` to `-mod=mod` so a clone with no `vendor/` builds, and add `vendor` to `.dockerignore` so the local build context stops shipping a dead vendor tree.
</objective>

<context>
Read the repo-root `Dockerfile`, `.dockerignore`, `.gitignore` and `Makefile.docker` before changing anything. The `hack/` directory does not exist yet.

Read the approved spec `specs/in-progress/009-cluster-build-arm.md`: Goal, Desired Behavior 2 and 6, Constraints, Acceptance Criteria 2, 3, 5 and 9, and Failure Modes rows 3-10.

Read `/home/node/.claude/plugins/marketplaces/coding/docs/go-dockerfile-guide.md` for Dockerfile conventions.

The five files and their upstream sources (public repositories, `origin/master` at copy time):

| Destination | Source repository | Source path | Mode |
|---|---|---|---|
| `hack/build-cluster.sh` | `https://github.com/bborbe/agent-task-executor` | `hack/build-cluster.sh` | `0755` |
| `hack/cluster-alias.sh` | `https://github.com/bborbe/agent-task-executor` | `hack/cluster-alias.sh` | `0755` |
| `hack/build-job.yaml` | `https://github.com/bborbe/agent-task-executor` | `hack/build-job.yaml` | `0644` |
| `hack/build-cluster-bootstrap.sh` | `https://github.com/bborbe/go-skeleton` | `hack/build-cluster-bootstrap.sh` | `0755` |
| `hack/dockerconfig.json.tpl` | `https://github.com/bborbe/go-skeleton` | `hack/dockerconfig.json.tpl` | `0644` |

`hack/build-cluster.sh` sources `hack/cluster-alias.sh` from its own directory and looks for `hack/build-job.yaml` there; `hack/build-cluster-bootstrap.sh` sources `hack/cluster-alias.sh` and reads `hack/dockerconfig.json.tpl` from its own directory. All five must land in `hack/` together.

Important environment note: this dark-factory run has `hideGit` effective, so the execution container masks the repo's `.git`. Do not use `git ls-tree` or other commands that read the target repo's `.git` in the verification section. Use `stat` and `find` for modes instead. Cloning the public upstream repositories into a temp directory is unaffected by the mask and is how the files are obtained.

Run this first to see the current state:

```bash
ls -la
cat .dockerignore
grep -n -- '-mod=' Dockerfile
```
</context>

<requirements>
1. Create `hack/` and copy the five files unmodified from their upstream repositories. Run from the repo root:

   ```bash
   tmp="$(mktemp -d)"
   git clone --depth=1 https://github.com/bborbe/agent-task-executor "$tmp/agent-task-executor"
   git clone --depth=1 https://github.com/bborbe/go-skeleton "$tmp/go-skeleton"

   mkdir -p hack
   install -m 0755 "$tmp/agent-task-executor/hack/build-cluster.sh" hack/build-cluster.sh
   install -m 0755 "$tmp/agent-task-executor/hack/cluster-alias.sh" hack/cluster-alias.sh
   install -m 0644 "$tmp/agent-task-executor/hack/build-job.yaml" hack/build-job.yaml
   install -m 0755 "$tmp/go-skeleton/hack/build-cluster-bootstrap.sh" hack/build-cluster-bootstrap.sh
   install -m 0644 "$tmp/go-skeleton/hack/dockerconfig.json.tpl" hack/dockerconfig.json.tpl

   cmp "$tmp/agent-task-executor/hack/build-cluster.sh" hack/build-cluster.sh
   cmp "$tmp/agent-task-executor/hack/cluster-alias.sh" hack/cluster-alias.sh
   cmp "$tmp/agent-task-executor/hack/build-job.yaml" hack/build-job.yaml
   cmp "$tmp/go-skeleton/hack/build-cluster-bootstrap.sh" hack/build-cluster-bootstrap.sh
   cmp "$tmp/go-skeleton/hack/dockerconfig.json.tpl" hack/dockerconfig.json.tpl

   rm -rf "$tmp"
   ```

   `cmp` must print nothing for every file. If any `cmp` reports a difference, stop: the copy is not unmodified. If a clone fails because the network is unavailable, stop and report that the files could not be copied; do not hand-write them. The spec requires the files to be copied unmodified, and a hand-written approximation is a different artifact.

2. Do not edit any copied file. In particular, do not change `hack/build-job.yaml`'s `automountServiceAccountToken: false`, its clone init container's security context, its build container's `runAsUser: 1000` / relaxed `seccompProfile` / `appArmorProfile`, its resource limits, or its `--opt build-arg:DOCKER_REGISTRY=docker.io` line. Those are the security boundary and the public-registry build argument the spec pins. Do not change the runner's whitelist, newline rejection, Job-name sanitization or `cluster-alias.sh` mapping.

3. Change the Dockerfile's Go build from vendor mode to module mode. In `Dockerfile`, the build stage has exactly this line:

   ```dockerfile
   RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -mod=vendor -ldflags "-s" -a -installsuffix cgo -o /main
   ```

   Replace it with:

   ```dockerfile
   RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -mod=mod -ldflags "-s" -a -installsuffix cgo -o /main
   ```

   Change nothing else in the Dockerfile. The cluster Job clones the repo without `vendor/` (the directory is gitignored), so `-mod=vendor` fails before the build starts; `-mod=mod` resolves modules over the network, which the cluster build pod can reach per the spec's Assumptions.

4. Add `vendor` to `.dockerignore`. The file currently contains `/data` and must keep it. Append a line containing exactly `vendor` (no leading slash), so the local build context stops shipping a dead `vendor/` now that Go ignores it under `-mod=mod`. The resulting file has two lines: `/data` and `vendor`.

5. Do not touch `Makefile.docker`. The sibling prompt owns the `BUILD_TARGET` dispatch that invokes `hack/build-cluster.sh`; this prompt only supplies the files it calls and the Dockerfile change it needs. Do not touch `README.md`, `CHANGELOG.md`, `docs/`, or `.gitignore` (the `.gitignore` change belongs to the docs prompt).
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The five `hack/` files are copied unmodified. Any later deviation is a deliberate, documented edit, not drift; this prompt makes none.
- The three scripts `build-cluster.sh`, `cluster-alias.sh` and `build-cluster-bootstrap.sh` are executable (`0755`). A plain `cp` can lose the bit, and the dispatch invokes them as `hack/<script>`; a lost bit fails at exec. Use `install -m 0755`.
- `build-job.yaml` and `dockerconfig.json.tpl` are not executable (`0644`).
- The Dockerfile's `-mod=mod` change affects the local arm too. That is expected: both arms build the same Dockerfile, and the local build resolves modules over the network instead of from the vendored tree.
- Do not add a `vendor/` directory, do not run `go mod vendor`, and do not remove `/vendor` from `.gitignore`.
- No new build knobs, no new config fields, no scenario. The cluster Job's limits, namespaces and credential mount are the upstream template's; do not tune them.
- The build container's relaxed sandbox profiles are deliberate and must not be copied to the clone init container (they are not; keep it that way).
</constraints>

<verification>
Run `make precommit` — must exit 0.

```bash
ls -l hack/
# expect: exactly these five files:
#   build-cluster.sh  cluster-alias.sh  build-job.yaml
#   build-cluster-bootstrap.sh  dockerconfig.json.tpl

stat -c '%a %n' hack/build-cluster.sh hack/cluster-alias.sh hack/build-cluster-bootstrap.sh
# expect: 755 for all three

stat -c '%a %n' hack/build-job.yaml hack/dockerconfig.json.tpl
# expect: 644 for both

find hack -maxdepth 1 -type f -perm -u+x | wc -l
# expect: 3

bash -n hack/build-cluster.sh
bash -n hack/cluster-alias.sh
bash -n hack/build-cluster-bootstrap.sh
# expect: exit 0 for all three
```

AC9 — the image builds from a clone with no `vendor/`:

```bash
grep -c -- '-mod=vendor' Dockerfile
# expect: 0

grep -c -- '-mod=mod' Dockerfile
# expect: >= 1

grep -c '^/vendor$' .gitignore
# expect: 1 (unchanged)

grep -c '^vendor$' .dockerignore
# expect: 1
```

The Job template's security boundary and public-registry build argument survived the copy:

```bash
grep -c 'automountServiceAccountToken: false' hack/build-job.yaml
# expect: 1

grep -c 'build-arg:DOCKER_REGISTRY=docker.io' hack/build-job.yaml
# expect: >= 1
```

Confirm the dispatch from the sibling prompt points at the runner (read-only check; do not edit it):

```bash
grep -cE '^[[:space:]]*hack/build-cluster\.sh' Makefile.docker
# expect: >= 1
```
</verification>
