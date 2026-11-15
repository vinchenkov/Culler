# Files

- [The operator dashboard](dashboard.md) - How the Bun dashboard server mirrors mc verbs over an authenticated loopback API, serves the browser modules and wiki exports, and how its smoke harness proves it.
- [The mc CLI, verb scope, and boundaries](mc-cli-and-verb-scope.md) - How the mc binary dispatches verbs, reports results and refusals, crosses into the helper container, derives caller scope from /mc/run.json, and which supporting packages define its policy.
- [The resident host daemon](resident.md) - The Bun process on the host that drives dispatch, starts containers, runs repository, trace, and wiki lanes, and supplies provider access through a host proxy. It reaches the spine through mc.
- [Runner containers and harness adapters](runners.md) - How the runner image is built, how the agent runner selects a harness adapter and reports back through the setuid mc, and how the Collie runner, fake harness, and shared runner modules fit in.
- [The spine: SQLite substrate and schema](spine-and-substrate.md) - How the spine database is defined, opened, guarded, and identified, and how the Go domain layer and the schema's triggers share the job of keeping state legal.
- [Culler system architecture](system-overview.md) - The process topology of a Culler deployment, how its components reach the one SQLite spine through the mc CLI, and where each component keeps its files.
