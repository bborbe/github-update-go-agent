# Per-phase timings (min) of one update-go Job from its captured pod log.
# Usage: python3 phases.py <pod.log>   (logs from cap.sh, kubectl logs --timestamps)
# Columns: pod start→clone clone clone→baseline-gates(base_e) base_e→plan bulk exec→gate gate gate→pr pr→end total
# Markers match on log message text, not file:line, so they survive code edits.
import os, sys, re, datetime as d

if len(sys.argv) != 2:
    sys.exit("usage: python3 phases.py <pod.log>")
path = sys.argv[1]
M = [
    ('start', '] github-update-go-agent'),
    ('clone_s', 'git ls-remote --symref'),
    ('clone_e', 'git clone+checkout succeeded'),
    ('base_e', 'planning: filtered'),
    ('plan_e', 'planning: claude CLI succeeded'),
    ('bulk_s', 'bulk_update_exec_start'),
    ('bulk_e', 'bulk_update_ok'),
    ('exec_s', 'spawning claude CLI'),
    ('gate_s', 'gate: running make'),
    ('gate_e', 'git status --porcelain'),
    ('pr', 'PR opened'),
    ('end', 'application finished'),
]
with open(path) as f:
    L = [l for l in f if re.match(r'^\S+ [IWE][0-9]{4}', l)]
T = {}
for l in L:
    for k, p in M:
        if p in l and k not in T:
            T[k] = d.datetime.fromisoformat(l[:26])


def s(a, b):
    return f"{(T[b] - T[a]).total_seconds() / 60:5.1f}" if a in T and b in T else "  -  "


label = os.path.basename(path).removesuffix('.log')
print(label, s('start', 'clone_s'), s('clone_s', 'clone_e'), s('clone_e', 'base_e'), s('base_e', 'plan_e'),
      s('bulk_s', 'bulk_e'), s('exec_s', 'gate_s'), s('gate_s', 'gate_e'), s('gate_e', 'pr'), s('pr', 'end'),
      s('start', 'end'),
      'exec_turns=%d' % sum('log-tool-use' in l for l in L),
      'plan_turns=%d' % sum('planning: tool_result' in l for l in L))
