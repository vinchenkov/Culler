# This image supplies Culler service containers and the private runtime volume.
# The tools stage downloads and installs each runtime part.
# The runtime stage copies the finished parts. The parts that change least come first.
ARG PROJECT_BASE=mc-project
FROM ${PROJECT_BASE} AS tools

# The private Node.js. Its npm serves only this stage.
ARG CULLER_NODE_VERSION=24.18.0
ARG CULLER_NODE_SHA256=58c9520501f6ae2b52d5b210444e24b9d0c029a58c5011b797bc1fe7105886f6
RUN install -d /opt/culler-runtime/node \
 && curl -fsSL "https://nodejs.org/dist/v${CULLER_NODE_VERSION}/node-v${CULLER_NODE_VERSION}-linux-arm64.tar.xz" -o /tmp/culler-node.tar.xz \
 && echo "${CULLER_NODE_SHA256}  /tmp/culler-node.tar.xz" | sha256sum -c - \
 && tar -xJf /tmp/culler-node.tar.xz -C /opt/culler-runtime/node --strip-components=1 \
 && rm -f /tmp/culler-node.tar.xz

# The browser tools and the full browser build with ffmpeg. The runtime holds no headless shell.
# Each browser program keeps no symbol table and no debug data. The browser folders keep no .pak.info files.
ARG BROWSER_DEPS=1
COPY --chmod=0644 runner/image/runtime-tools/package.json runner/image/runtime-tools/package-lock.json /opt/culler-runtime/browser/
RUN cd /opt/culler-runtime/browser \
 && /opt/culler-runtime/node/bin/node /opt/culler-runtime/node/lib/node_modules/npm/bin/npm-cli.js ci --omit=dev --ignore-scripts \
 && mkdir -p /opt/culler-runtime/ms-playwright \
 && if [ "$BROWSER_DEPS" = 1 ]; then \
      PLAYWRIGHT_BROWSERS_PATH=/opt/culler-runtime/ms-playwright /opt/culler-runtime/node/bin/node node_modules/playwright-core/cli.js install chromium --no-shell; \
    fi \
 && find /opt/culler-runtime/ms-playwright -type f -name '*.pak.info' -delete \
 && find /opt/culler-runtime/ms-playwright -type f -exec sh -ec 'for file; do if [ "$(head -c 4 "$file" | od -An -tx1 | tr -d " \n")" = 7f454c46 ]; then strip --strip-all "$file"; fi; done' strip-elf {} +

# OpenWiki without source maps and TypeScript type files.
COPY --chmod=0644 runner/openwiki/package.json runner/openwiki/package-lock.json /opt/culler-runtime/openwiki/
COPY --chmod=0644 runner/openwiki/patch-upstream.mjs runner/openwiki/project-documents.mjs /opt/culler-runtime/openwiki/
RUN cd /opt/culler-runtime/openwiki \
 && /opt/culler-runtime/node/bin/node /opt/culler-runtime/node/lib/node_modules/npm/bin/npm-cli.js ci --omit=dev --ignore-scripts \
 && /opt/culler-runtime/node/bin/node patch-upstream.mjs node_modules/openwiki \
 && find node_modules -type f \( -name '*.map' -o -name '*.d.ts' -o -name '*.d.mts' -o -name '*.d.cts' \) -delete \
 && /opt/culler-runtime/node/bin/node --input-type=module -e 'await import("./node_modules/openwiki/dist/integrations/core/session-manager.js"); await import("./node_modules/openwiki/dist/integrations/mcp/server.js")'

# The runtime Node.js keeps no npm and no corepack.
RUN rm -rf /opt/culler-runtime/node/lib/node_modules /opt/culler-runtime/node/bin/npm /opt/culler-runtime/node/bin/npx /opt/culler-runtime/node/bin/corepack

# The private Bun.
ARG CULLER_BUN_VERSION=1.3.12
ARG CULLER_BUN_SHA256=c40bc0ebca11bde7d75af497a654a874d0c7fd8d6a8d6031c173c10c9064297b
RUN curl -fsSL "https://github.com/oven-sh/bun/releases/download/bun-v${CULLER_BUN_VERSION}/bun-linux-aarch64.zip" -o /tmp/culler-bun.zip \
 && echo "${CULLER_BUN_SHA256}  /tmp/culler-bun.zip" | sha256sum -c - \
 && unzip -q /tmp/culler-bun.zip -d /tmp/culler-bun \
 && install -D /tmp/culler-bun/bun-linux-aarch64/bun /opt/culler-runtime/bun/bin/bun \
 && printf 'node %s %s\nbun %s %s\n' "$CULLER_NODE_VERSION" "$CULLER_NODE_SHA256" "$CULLER_BUN_VERSION" "$CULLER_BUN_SHA256" >/opt/culler-runtime/artifacts.txt \
 && rm -rf /tmp/culler-bun.zip /tmp/culler-bun

# The harness packages: the Claude Agent SDK with its Linux arm64 program, and Codex with its Linux arm64 program.
# The install omits the optional packages and the peer packages that no Culler code imports.
ARG RUNTIME_DEPS=0
COPY --chmod=0644 runner/agent-runner/package.json runner/agent-runner/bun.lock /opt/culler-runtime/app/
RUN if [ "$RUNTIME_DEPS" = 1 ]; then \
      cd /opt/culler-runtime/app && /opt/culler-runtime/bun/bin/bun install --production --frozen-lockfile --omit=optional --omit=peer; \
    elif [ "$RUNTIME_DEPS" != 0 ]; then exit 2; fi

FROM ${PROJECT_BASE} AS runtime
# The fixed directories and links.
RUN install -d /opt/culler-runtime/bin /opt/culler-runtime/commands /opt/culler-runtime/libexec /opt/culler-runtime/app /usr/local/libexec \
 && ln -s ../node/bin/node /opt/culler-runtime/bin/node \
 && ln -s ../bun/bin/bun /opt/culler-runtime/bin/bun \
 && ln -s /opt/culler-runtime/commands/mc /usr/local/bin/mc \
 && ln -s /opt/culler-runtime/commands/mc-land /usr/local/bin/mc-land \
 && ln -s /opt/culler-runtime/openwiki /opt/openwiki \
 && ln -s /opt/culler-runtime/app /app \
 && ln -s /opt/culler-runtime/libexec/mc-real /usr/local/libexec/mc-real
# 1. The private Node.js.
COPY --from=tools /opt/culler-runtime/node /opt/culler-runtime/node
# 2. The browser tools and the browser.
COPY --from=tools /opt/culler-runtime/browser /opt/culler-runtime/browser
COPY --from=tools /opt/culler-runtime/ms-playwright /opt/culler-runtime/ms-playwright
# 3. OpenWiki.
COPY --from=tools /opt/culler-runtime/openwiki /opt/culler-runtime/openwiki
# 4. The private Bun and the harness packages.
COPY --from=tools /opt/culler-runtime/bun /opt/culler-runtime/bun
COPY --from=tools /opt/culler-runtime/artifacts.txt /opt/culler-runtime/artifacts.txt
COPY --from=tools /opt/culler-runtime/app /opt/culler-runtime/app
# 5. The runtime source and the commands.
COPY --chmod=0755 runner/runtime-source/ /opt/culler-runtime/app/src/
COPY --chmod=0755 runner/image/mc-dispatch /opt/culler-runtime/commands/mc
COPY --chmod=0755 runner/image/mc-land /opt/culler-runtime/commands/mc-land
COPY --chmod=0755 runner/image/runtime-playwright /opt/culler-runtime/commands/playwright-cli
COPY --chmod=0755 runner/image/runtime-check.sh /opt/culler-runtime/bin/check
# 6. The files that change at each release.
ARG MC_BIN=mc
COPY --chown=10001:10001 --chmod=6755 runner/image/${MC_BIN} /opt/culler-runtime/libexec/mc-real
ARG CULLER_RELEASE_ID=development
RUN --mount=type=bind,source=runner/image/runtime-manifest.sh,target=/tmp/runtime-manifest.sh \
    printf '%s\n' "$CULLER_RELEASE_ID" >/opt/culler-runtime/release-id \
 && /bin/sh /tmp/runtime-manifest.sh >/opt/culler-runtime/manifest.json

# The runtime check reads the image as the agent account and writes no file.
ARG RUNTIME_DEPS=0
USER 10002:10002
RUN if [ "$RUNTIME_DEPS" = 1 ]; then /opt/culler-runtime/bin/check; fi
USER 0:0

FROM runtime AS product
