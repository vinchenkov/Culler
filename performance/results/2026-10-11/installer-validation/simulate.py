import hashlib, json, runpy, sys, tempfile, types
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0, "/private/tmp/culler-worktrees/mac-command-latency/performance")
m = runpy.run_path("/private/tmp/culler-worktrees/mac-command-latency/performance/install-helper.py")
with tempfile.TemporaryDirectory(prefix="installer-display-", dir="/private/tmp/culler-performance") as tmp:
 root=Path(tmp)
 (root/"bin").mkdir()
 candidate=b"\x7fELFdisplay-candidate"
 original=b"\x7fELFdisplay-original"
 binary=root/"bin/candidate"
 binary.write_bytes(candidate)
 installed=False
 def facts(_):
  data=candidate if installed else original
  return {"uid":10001,"gid":10001,"mode":"4755","size":len(data),"sha256":hashlib.sha256(data).hexdigest()}
 class Docker:
  def __init__(self,config): pass
  def verify(self): return {"helperID":"a"*64,"validationOnly":True}
 def run(argv,**kwargs):
  global installed
  if argv[1]=="exec": return types.SimpleNamespace(stdout=original)
  installed=True
  return types.SimpleNamespace(returncode=0,stderr=b"")
 def emit(value,stream=sys.stdout):
  print(json.dumps({**value,"validationOnly":True},sort_keys=True),file=stream,flush=True)
 with patch.dict(m["GUARDS"],{"read_config":lambda _: (root/"config.json",{"helperID":"a"*64}),"Docker":Docker,"emit":emit}), patch.dict(m["main"].__globals__,{"facts":facts}), patch.object(m["subprocess"],"run",side_effect=run), patch.object(sys,"argv",["install-helper.py","--config",str(root/"config.json"),"--binary",str(binary),"--record",str(root/"install.json")]):
  m["main"]()
