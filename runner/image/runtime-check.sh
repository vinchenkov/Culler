#!/bin/sh
set -eu
# The check writes no file: Bun keeps no transpiler cache.
export BUN_RUNTIME_TRANSPILER_CACHE_PATH=0
test "$(id -u)" = 10002
test ! -w /opt/culler-runtime/commands/mc
test ! -r /mc/spine
test "$(stat -c %u:%g:%a /opt/culler-runtime/libexec/mc-real)" = 10001:10001:6755
/opt/culler-runtime/bin/node --version
/opt/culler-runtime/bin/bun --version
/opt/culler-runtime/bin/bun -e '
  await import("/opt/culler-runtime/app/src/agent-runner/adapters/claude.ts");
  await import("/opt/culler-runtime/app/src/agent-runner/adapters/codex.ts");
'
# The runtime holds one Claude program, the glibc build for Linux arm64, and no unused peer package.
test -x /opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-arm64/claude
test ! -e /opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-arm64-musl
test ! -e /opt/culler-runtime/app/node_modules/@anthropic-ai/sdk
test ! -e /opt/culler-runtime/app/node_modules/@modelcontextprotocol
test ! -e /opt/culler-runtime/app/node_modules/zod
test -x /opt/culler-runtime/app/node_modules/@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/bin/codex
/opt/culler-runtime/bin/node --input-type=module -e 'await import("/opt/culler-runtime/openwiki/node_modules/openwiki/dist/integrations/core/session-manager.js")'
for role in agent-runner homie-runner setup-runner; do
  test -s "/opt/culler-runtime/app/src/$role/main.ts"
  test -r "/opt/culler-runtime/app/src/$role/main.ts"
done
