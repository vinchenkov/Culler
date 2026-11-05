#!/bin/sh
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$HERE/../.." && pwd)
python3 - "$ROOT" <<'PY'
import json,re,sys
from pathlib import Path
root=Path(sys.argv[1]); image=root/'runner/image'
project=(image/'Dockerfile').read_text(); runtime=(image/'runtime.Dockerfile').read_text()
for tool in ('go','bun'):
    version=re.search(r'^'+tool+r' = "([^"]+)"', (root/'.mise.toml').read_text(), re.M).group(1)
    assert 'ARG '+tool.upper()+'_VERSION='+version in project
assert 'FROM ubuntu:24.04@sha256:' in project
for tool in ('NODE','BUN','GO','UV','MISE','PYTHON'):
    assert re.search(r'ARG '+tool+r'_LINUX_ARM64_SHA256=[0-9a-f]{64}',project),tool
    assert '${'+tool+'_LINUX_ARM64_SHA256}' in project,tool
assert 'mise link' in project
assert 'mise-shim' not in project+runtime
for absent in ('mc-real','runner/openwiki','agent-runner','test_fake_routing'):
    assert absent not in project,absent

# Each tool step stores its own files once. No step changes the files of every tool.
assert 'chmod -R a+rX /opt /ms-playwright' not in project
assert not re.search(r'chmod -R .*\s/opt/?(\s|$)',project,re.M)
lines=project.splitlines()
def line_of(pattern):
    matches=[number for number,line in enumerate(lines,1) if re.search(pattern,line)]
    assert len(matches)==1,(pattern,matches)
    return matches[0]
def step_end(start):
    end=start
    while lines[end-1].endswith('\\'):
        end+=1
    return end
# Each version pin stands after the step of the previous tool and before its own step.
installs=[('UBUNTU_SNAPSHOT',r'apt-get install'),('GIT_VERSION',r'software/scm/git/'),
    ('PYTHON_VERSION',r'python-build-standalone/releases/'),('UV_VERSION',r'astral-sh/uv/releases/'),
    ('NODE_VERSION',r'nodejs\.org/dist/'),('BROWSER_DEPS',r'playwright-cli install-browser chrome-for-testing --with-deps'),
    ('MISE_VERSION',r'jdx/mise/releases/'),('GO_VERSION',r'go\.dev/dl/'),('BUN_VERSION',r'oven-sh/bun/releases/')]
previous_end=0
for argument,pattern in installs:
    declared=line_of(r'^ARG '+argument+'=')
    used=line_of(pattern)
    assert previous_end<declared<used,(argument,previous_end,declared,used)
    start=declared
    while not lines[start-1].startswith('RUN '):
        start+=1
    previous_end=step_end(start)
# The mise links read every tool pin, so they follow the last tool step.
mise_link=line_of(r'^RUN MISE_DATA_DIR=/usr/local/share/mise mise link ')
accounts=line_of(r'^RUN groupadd ')
manifest=line_of(r'project-manifest\.sh > ')
assert previous_end<mise_link<accounts<manifest
# The manifest step reads its script through a bind mount, so no layer holds the script.
assert 'COPY runner/image/project-manifest.sh' not in project
assert '--mount=type=bind,source=runner/image/project-manifest.sh' in project
# The apt step removes the setuid and setgid bits before the tool steps add files.
apt_start=line_of(r'^RUN printf ')
setuid=line_of(r'find / -xdev -type f .* -exec chmod a-s \{\} \+')
assert apt_start<setuid<=step_end(apt_start)<line_of(r'^ARG GIT_VERSION=')
git_step=lines[line_of(r'software/scm/git/')-1:step_end(line_of(r'software/scm/git/'))]
git_text=' '.join(git_step)
assert 'NO_GETTEXT=YesPlease' in git_text and 'INSTALL_STRIP=-s' in git_text
# The browser step strips the browser programs and removes the .pak.info files.
browser_start=line_of(r'^RUN cd /opt/project-tools ')
browser_text=' '.join(lines[browser_start-1:step_end(browser_start)])
assert 'npm_config_cache=/tmp/npm-cache npm ci' in browser_text
assert '-exec strip {} +' in browser_text and "-exec strip --strip-unneeded {} +" in browser_text
assert "-name '*.pak.info' -delete" in browser_text
# The folder mode changes alone, so the declaration files stay in their copy layer.
assert 'chmod 755 /opt/project-tools ' in browser_text
assert '/root/.npm' in browser_text and '/root/.cache' in browser_text and '/tmp/node-compile-cache' in browser_text
for extracted in ('/opt/python','/opt/uv-aarch64-unknown-linux-gnu','/opt/node','/opt/go','/opt/bun','/opt/project-tools/node_modules /ms-playwright','/usr/local/share/mise'):
    assert 'chmod -R a+rX '+extracted in project,extracted

assert re.findall(r'^FROM \S+ AS (\S+)$',runtime,re.M)==['tools','runtime','product']
for present in ('mc-real','runner/openwiki','runner/runtime-source','CULLER_NODE_VERSION','CULLER_BUN_VERSION'):
    assert present in runtime,present

# Each runtime version pin stands after the previous step and before the step that reads it.
rlines=runtime.splitlines()
def rline_of(pattern,nth=0):
    matches=[number for number,line in enumerate(rlines,1) if re.search(pattern,line)]
    assert len(matches)>nth,(pattern,matches)
    return matches[nth]
def rstep_end(start):
    end=start
    while rlines[end-1].endswith('\\'):
        end+=1
    return end
tools=rline_of(r'^FROM \S+ AS tools$')
node_pin=rline_of(r'^ARG CULLER_NODE_VERSION=')
node_step=rline_of(r'nodejs\.org/dist/')
browser_pin=rline_of(r'^ARG BROWSER_DEPS=')
browser_step=rline_of(r'^RUN cd /opt/culler-runtime/browser')
openwiki_step=rline_of(r'^RUN cd /opt/culler-runtime/openwiki')
npm_removal=rline_of(r'^RUN rm -rf /opt/culler-runtime/node/lib/node_modules')
bun_pin=rline_of(r'^ARG CULLER_BUN_VERSION=')
bun_step=rline_of(r'oven-sh/bun/releases/')
deps_pin=rline_of(r'^ARG RUNTIME_DEPS=')
install_step=rline_of(r'bun install --production --frozen-lockfile')
assert tools<node_pin<node_step<rstep_end(node_step)<browser_pin<browser_step<openwiki_step<rstep_end(openwiki_step)<npm_removal<bun_pin<bun_step<rstep_end(bun_step)<deps_pin<install_step
# The runtime stage copies the parts in order and ends with the files that change at each release.
runtime_stage=rline_of(r'^FROM \S+ AS runtime$')
links=rline_of(r'^RUN install -d /opt/culler-runtime/bin ')
copies=[number for number,line in enumerate(rlines,1) if line.startswith('COPY --from=tools ')]
sources=[rlines[number-1].split()[2] for number in copies]
assert sources==['/opt/culler-runtime/node','/opt/culler-runtime/browser','/opt/culler-runtime/ms-playwright','/opt/culler-runtime/openwiki','/opt/culler-runtime/bun','/opt/culler-runtime/artifacts.txt','/opt/culler-runtime/app'],sources
source_copies=[number for number,line in enumerate(rlines,1) if line.startswith('COPY --chmod=0755 ')]
mc_real=rline_of(r'^COPY --chown=10001:10001 --chmod=6755 .*mc-real$')
release_pin=rline_of(r'^ARG CULLER_RELEASE_ID=')
manifest=rline_of(r'runtime-manifest\.sh >')
check_pin=rline_of(r'^ARG RUNTIME_DEPS=',1)
check_step=rline_of(r'/opt/culler-runtime/bin/check; fi')
assert install_step<runtime_stage<links<copies[0] and copies[-1]<source_copies[0] and source_copies[-1]<rline_of(r'^ARG MC_BIN=')<mc_real<release_pin<manifest<check_pin<check_step
for directory in ('project-tools','runtime-tools'):
    package=json.loads((image/directory/'package.json').read_text())
    lock=json.loads((image/directory/'package-lock.json').read_text())
    assert lock['packages']['']['dependencies']==package['dependencies']
    for name,value in lock['packages'].items():
        if name:
            assert value.get('integrity') and value.get('resolved'),name
PY
printf 'Project tool declarations and private runtime declarations: ok\n'
