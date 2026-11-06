# Files

- [Agent input: directives, run brief, and shipped skills](agent-input.md) - What a Culler agent receives at the start of a run, where each part is authored, how it reaches the container, and the rules and tests that keep it consistent.
- [Failure controls and retry allowances](failure-controls.md) - How Culler classifies failed agent runs, which allowance each failure consumes, how retries are timed and restored, and how operator decisions and connection conditions stay separate.
- [Collie, the conversational tier](collie.md) - How Culler's lease-free conversational agent is started, scheduled, launched, fenced, and limited, including drafts, model changes, Vision conversations, and behavior during a pause.
- [Structured logging and log parity](logging.md) - How Go's mc/mclog and TypeScript's runner/shared/log.ts write one byte-identical NDJSON format, rotate shared files, and are held in parity by a shared golden fixture.
