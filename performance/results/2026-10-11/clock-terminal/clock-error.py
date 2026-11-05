import importlib.util,sys
from pathlib import Path
from types import SimpleNamespace
spec=importlib.util.spec_from_file_location("compare",Path("performance/compare.py"));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
def execute(args):
 worker=SimpleNamespace(variant={"name":"deadline","policy":"standard"},request=lambda identifier,argv:dict(type="result",id=identifier,argv=argv,elapsedMs=526,wallElapsedMs=1047526,code=2,valid=False,completedAt="2026-10-11T22:05:15.587Z"))
 m.run_trial(worker,sys.stdout,"trial",13,269,["task","list"],1)
m.execute=execute
sys.argv=["compare.py","--config","unused","--output","unused"]
raise SystemExit(m.main())
