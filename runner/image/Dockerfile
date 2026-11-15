# Project tools remain independent of the installed Culler runtime.
# Each step stores its own files once: it sets their modes and removes its caches before it ends.
# Each version pin stands before the first step that reads it, so a pin change rebuilds only the later steps.
FROM ubuntu:24.04@sha256:08571ca13e00ca07a2a84eab83a959b4242e22cceb16486a11bef1428c9e93a7 AS project
ARG UBUNTU_SNAPSHOT=20261001T000000Z

# The fixed Ubuntu snapshot selects exact system package artifacts.
# Archive signatures verify the certificate bootstrap before TLS trust exists.
# The step also removes the setuid and setgid bits of the system programs.
RUN printf 'Types: deb\nURIs: http://snapshot.ubuntu.com/ubuntu/%s/\nSuites: noble noble-updates noble-security\nComponents: main universe restricted multiverse\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n' "$UBUNTU_SNAPSHOT" >/etc/apt/sources.list.d/ubuntu.sources \
 && apt-get -o Acquire::https::Verify-Peer=false update \
 && DEBIAN_FRONTEND=noninteractive apt-get -o Acquire::https::Verify-Peer=false install -y --no-install-recommends ca-certificates \
 && apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      bash ca-certificates curl unzip xz-utils zip tar jq ripgrep tini libcap2-bin \
      build-essential pkg-config cmake ninja-build libssl-dev libcurl4-openssl-dev \
      libexpat1-dev zlib1g-dev gettext \
 && rm -rf /var/lib/apt/lists/* \
 && find / -xdev -type f \( -perm -4000 -o -perm -2000 \) -exec chmod a-s {} +

# Git installs stripped programs without message translations.
ARG GIT_VERSION=2.48.1
ARG GIT_SOURCE_SHA256=1c5d545f5dc1eb51e95d2c50d98fdf88b1a36ba1fa30e9ae5d5385c6024f82ad
RUN curl -fsSL "https://www.kernel.org/pub/software/scm/git/git-${GIT_VERSION}.tar.xz" -o /tmp/git.tar.xz \
 && echo "${GIT_SOURCE_SHA256}  /tmp/git.tar.xz" | sha256sum -c - \
 && tar -xJf /tmp/git.tar.xz -C /tmp \
 && make -C "/tmp/git-${GIT_VERSION}" -j2 prefix=/usr/local NO_TCLTK=YesPlease NO_GETTEXT=YesPlease INSTALL_STRIP=-s install \
 && rm -rf /tmp/git* \
 && git config --system safe.directory '*'

ARG PYTHON_VERSION=3.14.7
ARG PYTHON_RELEASE=20260901
ARG PYTHON_LINUX_ARM64_SHA256=8a0798baa8a2c27b5751d590aced543eaa85e20b3f73d93c1af049688acdc9c5
RUN curl -fsSL "https://github.com/astral-sh/python-build-standalone/releases/download/${PYTHON_RELEASE}/cpython-${PYTHON_VERSION}%2B${PYTHON_RELEASE}-aarch64-unknown-linux-gnu-install_only_stripped.tar.gz" -o /tmp/python.tar.gz \
 && echo "${PYTHON_LINUX_ARM64_SHA256}  /tmp/python.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/python.tar.gz -C /opt \
 && chmod -R a+rX /opt/python \
 && ln -s /opt/python/bin/python3 /usr/local/bin/python3 \
 && ln -s /opt/python/bin/python3 /usr/local/bin/python \
 && rm /tmp/python.tar.gz

ARG UV_VERSION=0.12.3
ARG UV_LINUX_ARM64_SHA256=bb66cb52e7b1823aed1183630d8d8e5c958840d584a4c55ec10a4cfc168dcca2
RUN curl -fsSL "https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/uv-aarch64-unknown-linux-gnu.tar.gz" -o /tmp/uv.tar.gz \
 && echo "${UV_LINUX_ARM64_SHA256}  /tmp/uv.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/uv.tar.gz -C /opt \
 && chmod -R a+rX /opt/uv-aarch64-unknown-linux-gnu \
 && ln -s /opt/uv-aarch64-unknown-linux-gnu/uv /usr/local/bin/uv \
 && ln -s /opt/uv-aarch64-unknown-linux-gnu/uvx /usr/local/bin/uvx \
 && rm /tmp/uv.tar.gz

ARG NODE_VERSION=24.18.0
ARG NODE_LINUX_ARM64_SHA256=58c9520501f6ae2b52d5b210444e24b9d0c029a58c5011b797bc1fe7105886f6
RUN curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-arm64.tar.xz" -o /tmp/node.tar.xz \
 && echo "${NODE_LINUX_ARM64_SHA256}  /tmp/node.tar.xz" | sha256sum -c - \
 && mkdir -p /opt/node \
 && tar -xJf /tmp/node.tar.xz -C /opt/node --strip-components=1 \
 && chmod -R a+rX /opt/node \
 && ln -s /opt/node/bin/node /usr/local/bin/node \
 && ln -s /opt/node/bin/npm /usr/local/bin/npm \
 && ln -s /opt/node/bin/npx /usr/local/bin/npx \
 && rm /tmp/node.tar.xz

# The browser programs keep no symbol tables, and the browser folders keep no .pak.info files.
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
ARG BROWSER_DEPS=1
COPY --chmod=0644 runner/image/project-tools/package.json runner/image/project-tools/package-lock.json /opt/project-tools/
RUN cd /opt/project-tools \
 && npm_config_cache=/tmp/npm-cache npm ci --omit=dev --ignore-scripts \
 && ln -s /opt/project-tools/node_modules/.bin/pnpm /usr/local/bin/pnpm \
 && ln -s /opt/project-tools/node_modules/.bin/playwright-cli /usr/local/bin/playwright-cli \
 && mkdir -p /ms-playwright \
 && if [ "$BROWSER_DEPS" = 1 ]; then \
      playwright-cli install-browser chrome-for-testing --with-deps; \
    elif [ "$BROWSER_DEPS" != 0 ]; then exit 2; fi \
 && find /ms-playwright -type f \( -name chrome -o -name headless_shell -o -name chrome_sandbox -o -name chrome_crashpad_handler \) -exec strip {} + \
 && find /ms-playwright -type f \( -name '*.so' -o -name '*.so.*' \) -exec strip --strip-unneeded {} + \
 && find /ms-playwright -type f -name '*.pak.info' -delete \
 && chmod 755 /opt/project-tools \
 && chmod -R a+rX /opt/project-tools/node_modules /ms-playwright \
 && rm -rf /var/lib/apt/lists/* /tmp/npm-cache /tmp/node-compile-cache /root/.npm /root/.cache

# The mise program reads only its own pin.
ARG MISE_VERSION=2026.10.0
ARG MISE_LINUX_ARM64_SHA256=4b8cacffac83e8493fc5d1eef25f6365edba73ccbed5a1f3987b7cb3f5079656
RUN curl -fsSL "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/mise-v${MISE_VERSION}-linux-arm64" -o /usr/local/bin/mise \
 && echo "${MISE_LINUX_ARM64_SHA256}  /usr/local/bin/mise" | sha256sum -c - \
 && chmod 0755 /usr/local/bin/mise

ARG GO_VERSION=1.25.12
ARG GO_LINUX_ARM64_SHA256=8b5884aef89600aef5b0b051fb971f11f49bb996521e911f30f02a66884f7bd2
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-arm64.tar.gz" -o /tmp/go.tar.gz \
 && echo "${GO_LINUX_ARM64_SHA256}  /tmp/go.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/go.tar.gz -C /opt \
 && chmod -R a+rX /opt/go \
 && ln -s /opt/go/bin/go /usr/local/bin/go \
 && ln -s /opt/go/bin/gofmt /usr/local/bin/gofmt \
 && rm /tmp/go.tar.gz

ARG BUN_VERSION=1.3.12
ARG BUN_LINUX_ARM64_SHA256=c40bc0ebca11bde7d75af497a654a874d0c7fd8d6a8d6031c173c10c9064297b
RUN curl -fsSL "https://github.com/oven-sh/bun/releases/download/bun-v${BUN_VERSION}/bun-linux-aarch64.zip" -o /tmp/bun.zip \
 && echo "${BUN_LINUX_ARM64_SHA256}  /tmp/bun.zip" | sha256sum -c - \
 && unzip -q /tmp/bun.zip -d /tmp \
 && install -D /tmp/bun-linux-aarch64/bun /opt/bun/bin/bun \
 && chmod -R a+rX /opt/bun \
 && ln -s /opt/bun/bin/bun /usr/local/bin/bun \
 && rm -rf /tmp/bun.zip /tmp/bun-linux-aarch64

# The mise links read every tool pin, so they follow every tool step.
RUN MISE_DATA_DIR=/usr/local/share/mise mise link "go@${GO_VERSION}" /opt/go \
 && MISE_DATA_DIR=/usr/local/share/mise mise link "bun@${BUN_VERSION}" /opt/bun \
 && MISE_DATA_DIR=/usr/local/share/mise mise link "node@${NODE_VERSION}" /opt/node \
 && MISE_DATA_DIR=/usr/local/share/mise mise link "python@${PYTHON_VERSION}" /opt/python \
 && chmod -R a+rX /usr/local/share/mise \
 && rm -rf /root/.cache

# Fixed accounts and protected mount locations form the supported platform contract.
RUN groupadd --gid 10001 mc-completion \
 && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin mc-completion \
 && groupadd --gid 10002 mc-model \
 && useradd --uid 10002 --gid 10002 --no-create-home --shell /usr/sbin/nologin mc-model \
 && install -d /workspace \
 && install -d -o 10002 -g 10002 -m 0700 /cache \
 && install -d -o 10002 -g 10002 -m 0700 /home/mc-model \
 && install -d -o 10002 -g 10002 -m 0700 /mc/attachments /mc/attachments/in /mc/codex \
 && install -d -o 10001 -g 10001 -m 0700 /mc/private /mc/private/attachments /mc/private/attachments/out /mc/spine \
 && mkdir -p /usr/share/culler-project \
 && dpkg-query -W -f='${Package}\t${Version}\t${Architecture}\n' > /usr/share/culler-project/system-packages.tsv

# The manifest step runs each tool once and removes the caches that those runs write.
RUN --mount=type=bind,source=runner/image/project-manifest.sh,target=/tmp/project-manifest.sh \
    /bin/sh /tmp/project-manifest.sh > /usr/share/culler-project/manifest.json \
 && rm -rf /tmp/node-compile-cache /root/.npm /root/.cache /root/.config
ENTRYPOINT ["/usr/bin/tini", "--"]
