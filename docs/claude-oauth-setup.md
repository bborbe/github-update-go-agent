# Claude OAuth Setup

The github-update-go-agent runner uses Claude Code CLI which requires OAuth credentials.
Credentials are stored on a PVC (`github-update-go-agent`) mounted at `/home/claude/.claude`.

## Prerequisites

- PVC `github-update-go-agent` exists in the target namespace
- Apply if missing: `cd agent/agent/claude/k8s && make apply`

## Setup (one-time per namespace)

Run a temporary pod with the PVC mounted:

```bash
kubectlquant -n <NAMESPACE> run claude-setup --rm -it \
  --image=node:22-alpine \
  --overrides='{
    "spec": {
      "containers": [{
        "name": "claude-setup",
        "image": "node:22-alpine",
        "stdin": true,
        "tty": true,
        "command": ["/bin/sh"],
        "volumeMounts": [{
          "name": "claude-data",
          "mountPath": "/home/claude/.claude"
        }]
      }],
      "volumes": [{
        "name": "claude-data",
        "persistentVolumeClaim": {
          "claimName": "github-update-go-agent"
        }
      }]
    }
  }'
```

Inside the pod:

```sh
export HOME=/home/claude
npm install -g @anthropic-ai/claude-code
claude login
# Follow browser auth flow
ls -la /home/claude/.claude/
# Verify: .credentials.json and settings.json exist
# The agent image runs as uid 1000 — hand the PVC to it, or the CLI can
# neither read the 0600 credentials nor refresh the token
chown -R 1000:1000 /home/claude/.claude
ls -ln /home/claude/.claude/
# Verify: every entry is owned by 1000 1000
exit
```

Pod auto-deletes (`--rm`). Credentials persist on the PVC.

## Migrating to the non-root image

Images before the non-root release ran as root, so an existing `github-update-go-agent`
PVC — and any Go cache PVC the Job mounts — holds root-owned files. Before the first
non-root image goes live in a namespace, run `chown -R 1000:1000` once over each such
PVC's mount root (same pod shape as above, root user). Without it every Job fails at
the Claude OAuth probe, before any repo work.

## Token Refresh

Claude Code refreshes OAuth tokens automatically. The `.credentials.json` file contains:

- `accessToken` — short-lived, auto-refreshed
- `refreshToken` — long-lived
- `expiresAt` — expiry timestamp

If the refresh token expires (rare), re-run the setup steps above.

## Verify

Check PVC contents from a temp pod:

```bash
kubectlquant -n <NAMESPACE> run claude-check --rm -it \
  --image=alpine \
  --overrides='{
    "spec": {
      "containers": [{
        "name": "claude-check",
        "image": "alpine",
        "stdin": true,
        "tty": true,
        "command": ["/bin/sh"],
        "volumeMounts": [{
          "name": "claude-data",
          "mountPath": "/home/claude/.claude"
        }]
      }],
      "volumes": [{
        "name": "claude-data",
        "persistentVolumeClaim": {
          "claimName": "github-update-go-agent"
        }
      }]
    }
  }'
```

```sh
cat /home/claude/.claude/.credentials.json | head -c 100
# Should show claudeAiOauth JSON
exit
```

## Namespaces

Repeat setup for each namespace where the agent runs:

- `dev` — development/testing
- `prod` — production
