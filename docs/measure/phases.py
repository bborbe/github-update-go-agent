import sys,re,datetime as d
M=[('start','main.go:153]'),('clone_s','git ls-remote --symref'),('clone_e','git clone+checkout succeeded'),('base_e','steps_planning.go:489]'),('plan_e','planning_runner.go:77]'),('bulk_s','bulk_update_exec_start'),('bulk_e','bulk_update_ok'),('exec_s','claude-runner.go:117]'),('gate_s','gate_runner.go:68]'),('gate_e','git status --porcelain'),('pr','PR opened'),('end','application finished')]
L=[l for l in open(sys.argv[1]) if re.match(r'^\S+ [IWE]1021',l)]
T={}
for l in L:
  for k,p in M:
    if p in l and k not in T: T[k]=d.datetime.fromisoformat(l[:26])
def s(a,b): return f"{(T[b]-T[a]).total_seconds()/60:5.1f}" if a in T and b in T else "  -  "
print(sys.argv[2][23:31], s('start','clone_s'),s('clone_s','clone_e'),s('clone_e','base_e'),s('base_e','plan_e'),s('bulk_s','bulk_e'),s('exec_s','gate_s'),s('gate_s','gate_e'),s('gate_e','pr'),s('pr','end'),s('start','end'), 'exec_turns=%d'%sum('log-tool-use' in l for l in L), 'plan_turns=%d'%sum('planning_runner.go:332' in l for l in L))
