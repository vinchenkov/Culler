# AgentsView acceptance

The AgentsView acceptance suite uses the pinned executable and an isolated deployment.
It requires the dashboard smoke dependencies and the executable for the pinned platform.
The container check requires Docker Desktop and a local Project image with `curl`.
`MC_BOUNDARY_IMAGE` selects that image. The default is `mc-prod`.
The container check uses the agent user and receives no credentials or host mounts.
It uses synthetic traces and no model credentials.
The suite checks the executable digest before it starts AgentsView.

The coding harness runs this command from the repository root:

```sh
MC_SMOKE_AGENTSVIEW_BIN=/absolute/path/to/agentsview \
  ./dashboard/smoke.sh acceptance/agentsview.test.ts
```

`MC_SMOKE_EVIDENCE_DIR` selects the evidence directory.
The default evidence directory is `culler-smoke-evidence` below the temporary directory.
The evidence records the AgentsView version and browser version.
`MC_SMOKE_COLOR_SCHEME=dark` selects the dark theme for a separate run.
The suite reports a failure when the executable or browser is unavailable.

The dashboard fast suite uses a local substitute to check the authentication boundary.
The browser acceptance suite checks the real AgentsView page.
Both results are required for complete acceptance.

## Remote browser

`MC_SMOKE_BROWSER_WS_ENDPOINT` selects an isolated Playwright browser server.
The smoke harness retains one browser connection per test process.
The connection exposes only loopback addresses from the test host to the browser.
The pinned AgentsView executable and the scratch dashboard remain on that host.

The browser server and its client must use the same Playwright version.
The Project image supplies the browser and the matching client packages under `/opt/project-tools/node_modules/`.
The server uses Playwright's `run-server` command and a port published only on `127.0.0.1`.
The server receives no production data or credentials.

The Node.js driver avoids a Bun WebSocket connection failure in the remote configuration.
`MC_SMOKE_PLAYWRIGHT_CORE` names the matching `playwright-core` package directory on the test host.
`MC_SMOKE_PLAYWRIGHT` selects `dashboard/acceptance/node-playwright.mjs` through its absolute path.
The default smoke configuration continues to use its local browser.

## Trace updates

The pinned viewer receives new messages through its event stream.
In the acceptance environment, the displayed transcript requires a reload to show those messages.
The same behavior occurs without the Culler proxy.
The acceptance check verifies the authenticated update response and the displayed content after reload.
