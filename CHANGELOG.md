# Changelog

This file holds one section for each Culler version, from the newest version to the oldest version.
The release workflow copies the section of a new version into the description of its GitHub release.
The [installation guide](INSTALL.md#versions) gives the version rules.

## 0.1.0

### Features

Culler 0.1.0 is the first public release.
Culler is a software factory that runs on your own Mac.
Its agents work toward your vision, on your repositories, through any AI provider.
You own the factory, the code, and the final say.

- **The factory line.** Five roles take each task to you. The Strategist proposes high-leverage tasks toward the `VISION.md` of the Project. The Arbiter promotes one proposal. The Worker submits one commit. The Verifier checks the commit against the acceptance criteria. The Packager writes the review packet.
- **Review packets.** You approve each task from its review packet before its commit reaches the `culler` branch of the Project.
- **Collie.** Collie is your Culler assistant. Define new tasks with Collie in the dashboard or from Discord.
- **The dashboard.** The dashboard holds the Board, the review packets, Collie, and the configuration of Culler and each Project.
- **OpenWiki.** The Scribe maintains a wiki for each Project. Agents use it as an index of the repository, and you use it to follow the Project as it changes.
- **AgentsView.** AgentsView shows the trace of each agent session.
- **Prepared environments.** The setup agent prepares the dependencies of each new Project and reports each environment limit.
- **Harnesses.** Agents run in Claude Code, Codex, or OpenCode. OpenCode connects other AI providers.
- **Multiple Projects.** Different Projects run at the same time.
- **Sandboxed agents.** Each agent run starts in a new container in Docker Desktop. A provider proxy adds your credentials to each model request, so agent containers hold no provider credentials.
- **Discord.** Optional. Talk with Collie and get alerts about blocked tasks.
- **Install and updates.** One script installs Culler and guides the setup. `culler update` installs a new release and keeps your data. An interrupted update can retry the same release or restore the previous release.

### Known limits

- Culler is in early access. A 0.x minor version can change the public interface.
- Culler supports one operator on one Mac with Apple silicon, and it requires Docker Desktop.
- Agent containers have open internet access. They can reach services on the loopback address of your Mac. Culler's own services require a token or a secret path.
