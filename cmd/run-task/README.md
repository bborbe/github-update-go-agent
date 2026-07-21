# Setup

```bash
CLAUDE_CONFIG_DIR=~/.claude-agent claude
```

# Run dummy task

```bash
make generate-dummy-task
make run-dummy-task
```

# Gotchas

- `CLAUDE_CONFIG_DIR` must point at a separately logged-in config dir. The
  Makefile already defaults it to `$(HOME)/.claude-agent` — don't override
  it to `$HOME/.claude` or leave it empty; that switches the `claude` CLI
  from macOS Keychain credential lookup to config-dir lookup and fails with
  `Not logged in`.
- Before running `make run-dummy-task`, run
  `unset ANTHROPIC_BASE_URL ANTHROPIC_MODEL ANTHROPIC_AUTH_TOKEN` in your
  shell. The Makefile's MiniMax-routing defaults (lines 21-23) use `?=`, so
  a value already exported in your session (e.g. from another project)
  silently wins over the Makefile default and routes this run to the wrong
  provider/model.
