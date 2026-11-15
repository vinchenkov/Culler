ARG BASE_IMAGE=culler-eval-base:unconfigured
FROM ${BASE_IMAGE}
RUN ["bun", "-e", "const fs=require('node:fs');for(const name of ['codex','codex-linux-arm64'])fs.rmSync('/app/node_modules/@openai/'+name,{recursive:true,force:true})"]
COPY cli/ /app/node_modules/@openai/codex/
COPY native/ /app/node_modules/@openai/codex-linux-arm64/
RUN ["/app/node_modules/@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/bin/codex", "--version"]
