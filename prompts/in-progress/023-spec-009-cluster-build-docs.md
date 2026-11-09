---
status: approved
spec: [009-cluster-build-arm]
created: "2026-11-08T23:32:54Z"
queued: "2026-11-09T00:14:27Z"
branch: dark-factory/cluster-build-arm
---

<summary>
- The README now documents both ways to build the image and the difference between them.
- It states that the cluster build requires an explicit tag and that the build-upload-clean chain is local-only.
- It points to the design document for the image and deployment story.
- The design document points back to the README build section.
- The changelog records the new cluster build capability under an Unreleased heading.
- The repository ignores the spec lock files dark-factory writes while a spec is in progress.
</summary>

<objective>
Document the two-arm build topology where operators and producers will look: a `## Build` section in `README.md` showing both invocations and their constraints, a cross-link from `docs/design.md` to that section, one `## Unreleased` changelog bullet, and the `/specs/in-progress/*.lock` ignore pattern.
</objective>

<context>
Read the repo-root `README.md`, `docs/design.md`, `CHANGELOG.md` and `.gitignore` before editing.

Read the approved spec `specs/in-progress/009-cluster-build-arm.md`: Desired Behavior 7, Constraints, and Acceptance Criterion 10. AC10's exact probes are repeated in the verification section; the prose below is shaped to satisfy them.

Read `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` for the `## Unreleased` placement and the one-bullet-per-logical-change convention, and `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md` for documentation style.

Current state:
- `README.md` has no `## Build` heading. Its headings include `## How It Works`, `## Env Vars`, `## Creating a New Agent`, `## Local Quick Test`, `## Links` and `## Related`. The header already contains a `Full design: [docs/design.md](docs/design.md)` line; the new cross-link must be inside the `## Build` section, not a second occurrence before it.
- `CHANGELOG.md` starts directly at `## v0.21.13`; there is no `## Unreleased` section.
- `.gitignore` already has `/vendor`, `/prompts/**/*.md.lock` and `/specs/log/`; it has no `/specs/in-progress/*.lock` line.
- `docs/design.md` § 2.5 Runtime describes the image.

This dark-factory run has `hideGit` effective, so the execution container masks the repo's `.git`. This prompt needs no git commands; use only `grep`, `awk`, `sed` and `make`.

Run this first to see the current anchors:

```bash
grep -n '^## ' README.md
grep -n 'design.md' README.md
grep -n '^## ' CHANGELOG.md | head -5
grep -n 'lock' .gitignore
grep -n '2.5 Runtime' docs/design.md
```
</context>

<requirements>
1. Add a `## Build` section to `README.md`, placed after `## Local Quick Test` and before `## Links`. Use exactly this content (the two fenced bash blocks are part of it):

````markdown
## Build

Two arms build the same image from the same checkout.

Local (the default) builds with the local Docker daemon, exactly as before:

```bash
make build
```

Cluster builds inside the sun cluster as a Kubernetes Job and pushes to Docker Hub at `docker.io/bborbe/github-update-go-agent`:

```bash
make build BUILD_TARGET=sun REF=<sha> VERSION=<tag>
```

The cluster arm requires `VERSION` on the command line or in the environment; it refuses to fall back to the Makefile's default (the newest release tag), because that would replace a released image. `REF` must be the full 40-character SHA of a commit that is already on `origin`.

`make buca` (build, upload, clean) is local-only: it needs a local Docker daemon. The cluster arm pushes from inside the Job, so there is nothing to upload or clean locally.

The image is consumed by `bborbe/nuke`; its mirror step copies the Docker Hub tag into the private registry when the pin is bumped.

See [docs/design.md](docs/design.md) for the image and its deployment story.
````

   Do not add a second `## Build` heading anywhere. Do not change the header's existing `Full design:` line.

2. Add one sentence to `docs/design.md` § 2.5 Runtime, at the end of the existing bullet, pointing at the README's build section. Append exactly:

   ```
   The image is built either on the local Docker daemon or inside the sun cluster as a Kubernetes Job; see the README's [Build section](../README.md#build) for both invocations.
   ```

   Keep the bullet a single line (the file's style is one bullet per line). Do not add a new heading.

3. Add `## Unreleased` to `CHANGELOG.md` directly above the existing `## v0.21.13` heading, with one blank line on each side. Under it, add exactly one bullet:

   ```
   - feat: add a cluster build arm — `make build BUILD_TARGET=<cluster> REF=<sha> VERSION=<tag>` builds and pushes the image inside the cluster as a Kubernetes Job, alongside the unchanged local `make build`
   ```

   Keep the bullet on one line. Do not edit any released section.

4. Add `/specs/in-progress/*.lock` to `.gitignore` on its own line. Keep every existing entry, including `/vendor`. The consumer is dark-factory: it writes `specs/in-progress/<id>-<name>.md.lock` while a spec is in progress, and the prompt lane's `/prompts/**/*.md.lock` has no `specs` equivalent, so without this line the first approved spec leaves an untracked lock file in the tree.

5. Do not modify any `.go` file, `Makefile`, `Makefile.docker`, `Dockerfile`, `.dockerignore`, or the `hack/` files. This prompt is documentation and ignore-file only.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The `## Build` heading must appear exactly once, and the literal line `make build BUILD_TARGET=sun REF=<sha> VERSION=<tag>` must appear in the README.
- The README's `## Build` section must contain exactly one line mentioning `design.md`; the header's existing `Full design:` line is outside the section and does not count, so do not add a second `design.md` line inside the section.
- The CHANGELOG's `## Unreleased` section must contain exactly one bullet line (`- ` at the start of the line). Do not wrap it onto a continuation line that starts with `- `.
- Keep the changelog's `feat:` prefix: this is new capability.
- `.gitignore` keeps every existing entry, including `/vendor`.
- No new build knobs or code changes in this prompt. The documented behavior must match what the two sibling prompts implement: local arm unchanged; cluster arm requires a caller-supplied `VERSION`; `buca` is local-only; both arms push `docker.io/bborbe/github-update-go-agent`.
- No new scenario.
</constraints>

<verification>
Run `make precommit` — must exit 0.

AC10 — the documented build path matches reality:

```bash
grep -c '^## Build' README.md
# expect: 1

grep -c 'make build BUILD_TARGET=sun REF=<sha> VERSION=<tag>' README.md
# expect: >= 1

grep -c 'make build' README.md
# expect: >= 2

grep -ciE 'buca.*local' README.md
# expect: >= 1

awk '/^## Build/{f=1} f&&/design\.md/{c++} END{print c+0}' README.md
# expect: 1

grep -c '^/specs/in-progress/\*\.lock$' .gitignore
# expect: 1

awk '/^## Unreleased/{f=1;next} /^## /{f=0} f&&/^- /{c++} END{print c+0}' CHANGELOG.md
# expect: 1
```

Confirm the cross-link is inside the Build section and points at the README:

```bash
sed -n '/^## Build/,$p' README.md | grep -c 'docs/design.md'
# expect: 1

grep -c 'Build section' docs/design.md
# expect: 1
```

Confirm the local arm's documented invocation and the cluster invocation both appear:

```bash
grep -n 'make build' README.md
# expect: one bare `make build` and one `make build BUILD_TARGET=sun REF=<sha> VERSION=<tag>`
```
</verification>
