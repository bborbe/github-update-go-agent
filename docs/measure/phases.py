# Per-phase timings (min) of one update-go Job from its captured pod log.
# Usage: python3 phases.py <pod.log>   (logs from cap.sh, kubectl logs --timestamps)
# Columns: start→clone clone clone→baseline-gates base_e→plan bulk exec→gate gate gate→pr pr→end total
# Markers match on log message text, never file:line, so they survive code edits.
# The baseline gates (planning) and the execution gate both log "gate: running
# make", but only the execution gate logs it from gate_runner.go:68 (the
# planning baseline uses RunTargetFull at gate_runner.go:93) — so the marker
# below is execution-only.
import os, sys, re, datetime as d

if len(sys.argv) != 2:
    sys.exit("usage: python3 phases.py <pod.log>")
path = sys.argv[1]
with open(path) as f:
    L = [l for l in f if re.match(r'^\S+ [IWE][0-9]{4}', l)]


def find(marker, after=None, before=None, last=False):
    hits = [d.datetime.fromisoformat(l[:26]) for l in L if marker in l]
    if after is not None:
        hits = [t for t in hits if t > after]
    if before is not None:
        hits = [t for t in hits if t < before]
    if not hits:
        return None
    return hits[-1] if last else hits[0]


T = {
    'start': find('] github-update-go-agent'),
    'clone_s': find('git ls-remote --symref'),
    'clone_e': find('git clone+checkout succeeded'),
    'plan_e': find('planning: claude CLI succeeded'),
    'bulk_s': find('bulk_update_exec_start'),
    'bulk_e': find('bulk_update_ok'),
    'exec_s': find('spawning claude CLI'),
    'gate_s': find('gate_runner.go:68] gate: running make'),
    'pr': find('PR opened'),
    'end': find('application finished'),
}
# End of the planning baseline gates = the last baseline gate that completed
# before the planning LLM ran.
T['base_e'] = find('gate: make check succeeded', before=T['plan_e'], last=True)
# End of the execution gate = first working-tree status read after it started.
T['gate_e'] = find('git status --porcelain', after=T['gate_s'])


def s(a, b):
    ta, tb = T[a], T[b]
    return f"{(tb - ta).total_seconds() / 60:5.1f}" if ta and tb else "  -  "


print(os.path.basename(path).removesuffix('.log'),
      s('start', 'clone_s'), s('clone_s', 'clone_e'), s('clone_e', 'base_e'), s('base_e', 'plan_e'),
      s('bulk_s', 'bulk_e'), s('exec_s', 'gate_s'), s('gate_s', 'gate_e'), s('gate_e', 'pr'), s('pr', 'end'),
      s('start', 'end'),
      'exec_turns=%d' % sum('log-tool-use' in l for l in L),
      'plan_turns=%d' % sum('planning: tool_result' in l for l in L))
