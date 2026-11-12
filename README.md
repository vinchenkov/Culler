<p align="center">
  <img src="docs/logo-full.svg" alt="The Culler logo: a chick and two eggs in a nest, next to the word Culler, on a navy tile">
</p>

**Software factory at home**

Culler is a project factory that runs on your own Mac. You own the factory, the code, and the final say. Its agents work toward your vision, on your repositories, with your ChatGPT or Claude account or an API key from a supported provider.

![The factory line. One task moves from VISION.md through five numbered roles: 01 Strategist, 02 Arbiter, 03 Worker, 04 Verifier, and 05 Packager. Below each role, the db records the new stage of the task: Proposed, Seeded, Worked, Verified, and Packaged. At the end, you approve the packet, the db records Approved, and the change goes to the main branch.](docs/hero-dark.svg)

[Install](#quick-install)  ·  [The factory line](#the-factory-line)  ·  [Architecture](#architecture)  ·  [Security](#security)

---



## Philosophy

Attention is sacred, they say. Culler helps build projects with that in mind.

You define a **vision** that states your project's open-ended, high-level goal, and Culler proactively proposes high-leverage work toward your stated intents and submits it for your review.

Additionally, you can define new work yourself or with the help of Collie, your Culler assistant.

You can review, approve, revise, cancel, and otherwise **cull** work as agents move tasks along the factory line.

---



## The factory line

You can import an existing project or start one from scratch.

### Review only validated work

Five roles run in order, with one agent run at a time for each Project.

1. **Strategist** proposes high-leverage task candidates to advance your vision.
2. **Arbiter** judges each proposal and promotes the most effective one.
3. **Worker** works the task in its own repository copy and submits one commit.
4. **Verifier** checks the work against acceptance criteria and asks Worker to correct any gaps.
5. **Packager** creates a *review packet meant for a human mind.*

One packet for each decision.

---



## Features

**Proactive work.** Culler does work in between your decisions, with up to three review packets open for each Project by default.

**Illustrative review packets.** Maintain effective command of your projects by never losing sight of them.

**OpenWiki.** Culler keeps an [OpenWiki](https://github.com/langchain-ai/openwiki) wiki for each project. An agent named **Scribe** updates it after new commits reach `main`, at most once per hour by default. It serves two purposes:

1. Every agent, including the Strategist, reads it to understand the repository: how its files relate, which rules hold, and why each boundary exists.
2. You can read it in a viewer to re-familiarize yourself with your project as it continually develops.

**AgentsView.** A trace viewer for agent observability, from the [AgentsView](https://github.com/kenn-io/agentsview) project.

**Prepared environments.** When you start a project, the setup agent prepares the Project dependencies and reports each environment limit. A Project image change waits for your approval.

**Tried and true harnesses.** Culler leverages frontier-lab and community-vetted harnesses (Codex, Claude Code, OpenCode). Each Culler release pins a version of each harness, so their gains reach you with Culler updates.

**Multiple Projects.** Different Projects run at the same time.

**Discord integration.** Stay in the loop when you're away from your work device. Talk with Collie from Discord and get alerts about blocked tasks. Optional.

---



## Quick Install



### One script installs Culler and guides the setup.

```sh
curl -fsSL https://raw.githubusercontent.com/vinchenkov/Culler/main/install.sh | bash
```

The terminal setup has six stages:

1. Welcome
2. Prepare your Mac
3. Connect an agent
4. Review your setup
5. Start Culler
6. Open your dashboard

Setup asks for your approval before it starts Culler services.
Then you add your first Project in the dashboard.

## Usage

1. In the dashboard, select **Add Project**. Import an existing repository or start a new one.
2. Write the Project's vision, select **Save Vision**, then select **Initialize project**. The setup agent prepares the Project environment.
3. Culler proposes, builds, and checks tasks. You can add your own tasks on the Board, or ask Collie.
4. Review each packet, then approve, revise, or cancel it. Approved work merges into `main`.

## Architecture

**Each agent runs in a sandbox.
Host services and the dashboard run on your Mac.
A proxy ensures AI provider credentials don't reach agents.**

![FIG. 3, architecture. On your Mac, the Browser opens the dashboard. The dashboard reaches AgentsView through a local proxy and serves OpenWiki. The dashboard and the resident run mc. mc reaches the db through docker exec and the helper container. Inside Docker Desktop, the db is one SQLite database in a named volume. The resident starts each agent container. Each agent container runs mc inside the container and reads and writes the db. The Project repositories supply a fresh source workspace for each run, mounted at /workspace/source, and keep the result commit after the run. Provider credentials stay in the macOS Keychain. mc reads them for the provider proxy in the resident. Agent containers send model requests with a temporary Culler token to the proxy. The proxy adds the credentials and sends each request to the model provider. Agent containers hold no provider credentials. Discord is optional.](docs/architecture-dark.svg)

One program writes to the db.

- `mc` (Mission Control) is the Go command line program of Culler. It owns dispatch and all Culler state, and it is the only writer of the db. Agents, the resident, and the dashboard run it; you never run it yourself.
- **The db** is one SQLite database in a Docker named volume. Triggers and CHECK constraints refuse illegal states.
- **The resident** is the host daemon. It runs a dispatch tick at start and then every 60 seconds by default. It starts the containers that need to run next. Its provider proxy adds the provider credentials to each model request.
- **The dashboard** is the web interface at `127.0.0.1:7333`. It runs `mc` for each db read and each db write.
- **Agent containers** run the agents. Each pipeline run and setup operation gets a new container, and each Collie conversation gets its own. The runner starts the agent harness with the Project tools. Worker, Verifier, Packager, and Scribe runs work in a fresh source workspace of the Project repository at `/workspace/source`. The Project home at `/home/agent` and the `/cache` folder carry over between runs. A container holds a temporary Culler token and no provider credentials.
- **The macOS Keychain** holds the provider credentials. `mc` reads them on the host for the provider proxy.
- **AgentsView** shows the trace of each run and each Collie conversation. Trace links in the dashboard open it.
- **OpenWiki** shows the wiki of each Project. The dashboard serves it on port 7335 below a secret path, and the **Wiki** link of each Project opens it.

launchd keeps the three host services alive: the resident, the dashboard, and AgentsView.

## Security

**All agents run in sandboxed environments via Docker Desktop.**

**Process isolation** — agent containers run in the Docker Desktop Linux virtual machine, with all Linux capabilities dropped.  
**Filesystem isolation** — only Project and Culler administrative folders are mounted into containers. No other host files are touched.  
**Non-root execution** — agents run as the unprivileged user 10002.  
**Per-run containers** — each pipeline run and setup operation gets a new container, and each Collie conversation gets its own.

Culler has these security limits today:

- Agent containers have open internet access.
- An agent can read its temporary Culler token and the Project secrets you configure.
- Agent containers can reach any service on the loopback address of your Mac. Culler's own services require a token or a secret path.
- The Project home and the `/cache` folder carry over between runs, so one run can leave files for the next.
- The harnesses run without their own sandboxes. The container is the only boundary.
- The initial setup agent writes directly to the Project folder.
- Collie can read every Project folder.
- Agent containers have no CPU or memory limit by default. You can set limits on the dashboard's Advanced settings page.

---



## Requirements

- A Mac with Apple silicon.
- Docker Desktop, installed and running. Setup checks it but does not install it.
- Apple's developer tools, which include Git. The installer starts their install when they are absent; run the installer again after it completes.
- A ChatGPT or Claude account, or an API key from a supported provider.

Culler downloads its own copy of Bun.

---



## Roadmap

**Culler is in early access**.

For v1 the targets are:

#### More effective and efficient agents

Particularly Strategist/Arbiter and Packager provide most of Culler's distinct agent value.

Benchmark and run GEPA ([https://github.com/gepa-ai/gepa](https://github.com/gepa-ai/gepa)) optimization against each agent's directive.

#### Connectors and MCPs

Give agents the ability to query data and leverage capabilities from additional services.

#### Worksources

Allow Strategist to seed work from said connected data sources, such as Gmail, Slack, Linear, etc.

Have Scribe use supplemental data sources to help curate the project's OpenWiki knowledgebase over time to better develop strategy.

#### Pluggable custom agent roles
Instead of Strategist alone proposing work, define custom agent roles.
Maybe roles like: Trimmer - to identify unused code, Optimizer - to target performance enhancement, etc.

#### Quality of life

Just make Culler more pleasant and reliable.

---

## License

MIT. See [LICENSE](LICENSE).

---
