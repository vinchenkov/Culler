# Directive evaluation

## Purpose

Culler assigns agent roles to produce useful work with limited operator attention.
The Packager explains a completed change and the evidence that supports it.
A useful packet lets the operator understand the change and its limits.
The operator must develop an accurate mental model of the change from the packet alone.
The explanation must support desktop review without source-code inspection.
It must reduce cognitive load while preserving a representative account of the change.

The evaluation framework must measure whether directive edits improve that result.
It must preserve the evidence for each candidate and decision.
It must support later comparisons against an adopted directive.

## Document authority

This specification is the implementation brief for the framework.
Its requirements include the experiment sequence and integration boundaries.
Implementation must require no research viewer or conversation history.
The [experiment inputs](#required-experiment-inputs) distinguish unresolved configuration from framework requirements.

## Scope

The first release must evaluate and evolve only the Packager directive.
Its editable text must include the full instructions from the shared `writing` skill.
It must provide a complete proof of concept for that role.
Later releases can add one role at a time.
Each additional role requires its own cases and assessment criteria.

The framework must reside under `evals/` in this repository.
Its dependencies and assets must remain outside product releases.
The deployed product must remain independent of the framework.
Framework delivery must preserve the production source files.

The framework can read product sources and reuse available product helpers.
Temporary test helpers can exercise private product behavior.
The tracked helper sources must remain under `evals/`.

Each trial must execute one isolated Packager session.
Fixed upstream artifacts must supply the results of the other roles.
The first release must assess packet production from supplied evidence.
Ordinary tests must keep the fake harness for each role.
Model evaluations must use a separate developer command.
The current fast suite must remain token-free.

Directive adoption must remain separate from framework delivery.
An adopted directive must enter its normal source file through a reviewed change.
An adopted Packager directive must include its evolved writing guidance.
Its production input must use the same guidance placement and skill set as the evaluated input.
Other roles must retain the shared `writing` skill unchanged.
The user controls Git publication.

## Terms

The framework uses Genetic-Pareto (GEPA) for directive evolution.
GEPA controls the evolution algorithm.
The optimizer is the model that proposes directive edits.

| Term | Definition |
| --- | --- |
| Experiment | An experiment includes preparation, one GEPA run, and an independent final comparison. |
| Protocol | A protocol fixes the rules for a directive comparison. |
| Case | A case contains fixed role inputs and authoritative reference facts. |
| Case type | A case type identifies the explanation that a change requires. Each type can contain several independent cases. |
| Trial | A trial executes one directive on one case in a fresh session. |
| Training set | The training set supplies cases for feedback and comparisons between parents and children. GEPA calls it `trainset`. |
| Validation set | The validation set supplies scores for parent selection and the returned candidate. GEPA calls it `valset`. |
| Final holdout set | The final holdout set supplies independent cases after GEPA returns a candidate. |
| Minibatch | A minibatch is a subset of training cases for one proposal task. |
| Candidate | A candidate contains one complete proposed directive. |
| Original directive | The original directive contains the Packager instructions and merged `writing` instructions before optimizer edits. |
| Seed candidate | The seed candidate is the original directive that initializes the GEPA run. |
| Parent and child | A parent supplies the directive for an edit. The proposed directive is its child. |
| Component | A component is a named text field that GEPA can edit. This experiment exposes one complete directive component. |
| Proposal task | A GEPA proposal task identifies a parent, minibatch, and component. It differs from a Culler Project task. |
| Iteration | An iteration contains proposal tasks, their assessments, and the resulting candidate pool update. |
| GEPA run | A GEPA run extends from seed validation to the returned candidate. |
| Candidate pool | The candidate pool contains the seed and accepted candidates with their stored validation scores. |
| Case score | A case score aggregates repeated trials for one directive and case within one evaluation. |
| Optimizer | The optimizer is the model that proposes directive edits from permitted case feedback. |
| Evaluator | The evaluator is code that runs trials, invokes assessments, and records results. |
| Judge | The judge is a model that assesses packet quality and supplies scores with explanatory feedback. |
| Role adapter | A role adapter supplies role-specific inputs, execution behavior, and assessment requirements to the evaluator. |
| GEPA adapter | The GEPA adapter connects candidate evaluation and feedback to the GEPA library. |
| Command adapter | The command adapter reproduces the permitted mission control command (`mc`) behavior inside a trial. |
| Pilot | The pilot checks case execution, assessment quality, and variation before the GEPA run. |

GEPA documentation calls the optimizer the reflection model.
This specification uses optimizer for that model throughout.
A trial can contain several model calls and subagent calls.
Those calls belong to one trial and do not create additional independent cases.

## Framework architecture

The evaluator must connect these components through versioned records:

| Component | Source | Required responsibility |
| --- | --- | --- |
| Evolution algorithm | GEPA library | GEPA selects parents, compares proposals, updates validation records, and returns a candidate. |
| GEPA adapter | New code under `evals/` | The adapter converts evaluation requests into trials and returns case scores with diagnostic evidence. |
| Optimizer integration | New code under `evals/` | The integration invokes Codex with permitted feedback and captures the complete proposed directive. |
| Case preparation | New code under `evals/` | Case preparation verifies external artifacts and constructs isolated trial inputs. |
| Packager role adapter | New code under `evals/` | The role adapter supplies the Packager environment and its assessment contract. |
| Input and harness helpers | Product sources where isolation permits reuse | Helpers supply brief structure, skills, harness invocation, and trace capture. |
| Command adapter | New code under `evals/` | The adapter provides case responses and enforces the Packager command contract. |
| Packet display | Product wrapper with an evaluation browser host | The display preserves the product's packet restrictions and desktop appearance. |
| Judge integration | New code under `evals/` | The integration invokes Claude Code for browser assessment and structured scores with feedback. |
| Experiment records and views | New records and browser viewer | Portable records preserve evidence. The browser viewer shows chronological events, candidate ancestry, scores, and artifacts. |

The evaluator must separate role behavior from shared experiment control.
The GEPA adapter must consume case scores independently of packet-specific assessment logic.
The implementation must use GEPA's evolution algorithm through its supported interfaces.

### Product integration boundaries

The selected Culler revision must supply these contracts:

| Contract | Product source |
| --- | --- |
| Packager instructions | `mc/verbs/directives/packager.md` |
| Shared writing guidance | `mc/verbs/skills/shared/writing/SKILL.md` |
| Role skills and skill pointers | `mc/verbs/skills/packager/` and `mc/verbs/roleskills.go` |
| Run brief | `mc/verbs/brief.go` |
| Harness invocation and traces | `runner/agent-runner/adapters/` |
| Skill installation | `runner/agent-runner/skills.ts` |
| Packet wrapper and frame restrictions | `dashboard/src/ui/review.js` |
| Product command validation and scratch schema | `mc/verbs/` and `mc/substrate/schema.sql` |

The framework must reuse isolated helpers at these integration points.
Reuse must preserve trial isolation and the selected revision's relevant behavior.
An evaluation wrapper must supply synthetic identity and private state to each reused helper.
The wrapper must exclude deployed Culler heartbeats and production terminal calls.
Any necessary substitute must have parity checks and an explicit result limitation.

### State ownership

Each trial must have its own command state and output locations.
The command adapter must restore that state from the fixed case.
Ordinary trials must use the adapter without a deployed Culler pipeline.
Disposable product state must support command parity checks through `mc`.

Experiment records must reside outside the production spine.
Local MLflow state must remain separate from trial command state and product state.
Portable records and artifacts must suffice to reconstruct experiment views without the original MLflow database.

## Experiment configuration

The framework must use GEPA for automatic directive evolution.
The browser viewer must show chronological events and candidate ancestry.
Local MLflow can index the experiment records.
Portable experiment records must preserve the evidence independently of MLflow.

The experiment must identify the exact source revision and dependency versions.
The first Packager experiment must use these model and harness assignments:

| Role | Model | Harness | Effort | Responsibility |
| --- | --- | --- | --- | --- |
| Packager | GPT-6.1 Sol | Codex | high | The Packager produces the packet. |
| Judge | Claude Opus 5.5 | Claude Code | high | The judge assesses packet quality and supplies dimension scores with feedback. |
| Optimizer | GPT-6.1 Sol | Codex | high | The optimizer proposes directive edits from permitted feedback. |

These model names state the selected configurations.
The experiment configuration must resolve each name to an exact model identifier that its harness accepts.
The experiment must record the requested and effective model identifiers, harness versions, and effective model settings.
An ordinary authenticated trial can discover an unavailable model assignment.
The evaluator must record that failure and preserve the selected model assignment.
The model assignments and harness settings must remain fixed across candidate comparisons.
Each role must use an immutable container image digest or full local image identifier.
The runtime must record the effective image identity, credential channel, and endpoint without authentication values.

Each role must declare `runtime.<role>.effort` before model execution.
The runtime must pass that effort explicitly to the selected harness.
Codex must use the same effort as its default for subagents.
Claude Code must receive its explicit effort option and environment override.
The browser must expose each role's effort and permit only values that its harness accepts.
Model support can restrict these values further.

Each session must record these distinct facts:

- The requested effort comes from the fixed protocol.
- The configured effort comes from the harness configuration record.
- The observed effort comes from native traces, when those traces supply it.

An unavailable observation must remain explicit.
The evaluator must reject a missing or different configured effort.
It must reject an observed effort that differs from the fixed protocol.
The fixed protocol and its hash must include each role's effort.
The Judge assessment context must include its effort.
An effort change must produce a distinct experiment identity and invalidate incompatible assessments.
Codex and Claude Code roles must expose a boolean fast mode setting in the public configuration and browser.
All saved presets must enable fast mode for the Packager, Judge, and Optimizer.
A fast mode setting must preserve the model, effort, prompt, and score rules.
Claude Code must receive an explicit session setting for both enabled and disabled fast mode.
Its account availability check must remain active when fast mode is enabled.
The runtime must record the requested setting and configured setting.
The Codex record must also include the configured service tier.
The runtime must preserve Claude Code's reported fast mode states and reasons when available.
A fallback to standard speed must preserve the assessment and its runtime evidence.
A configuration record must remain distinct from proof of the effective service tier.
Fast mode must use the same rubric and case sets as standard mode.
The exact protocol snapshot must retain the setting as execution information.

Historical records without effort must display `Effort unrecorded`.
The framework must preserve the historical records without an inferred effort.

The Packager and optimizer must use separate sessions and permitted inputs despite their shared model and harness.
The judge's Claude Code environment must preserve the browser inspection and evidence access rules in this specification.

The evaluator must invoke the judge for each packet assessment.
The evaluator must apply the fixed aggregation rule to the judge's scores.
The evaluator must provide the scores and diagnostic feedback to GEPA.

Each assignment must support a configurable model, provider, and harness.
Each result claim must identify the role model and harness that the experiment evaluates.

The protocol must fix these conditions before candidate search:

- The case groups and their dataset assignments must have recorded versions.
- The role input and tool permissions must have recorded versions.
- The rubric and reference facts must have recorded versions.
- The judge configuration must have a recorded version.
- The aggregation method and comparison rules must have recorded versions.
- The retry policy and experiment completion rule must have recorded versions.

The compared directives must receive equivalent execution conditions.
The framework must record intentional differences from the deployed role environment.
A material difference must limit the result claim to the tested configuration.

The experiment must record actual token use and duration.
It must record cost when the provider supplies sufficient usage data.
Unavailable usage data must remain explicit.

### Readiness for model trials

The minimum evaluator path must pass checks without model calls before the first model trial.
Those checks must demonstrate these outcomes:

- Case preparation restores the fixed repository, evidence, and private command state.
- Input assembly supplies the complete directive, brief, and permitted skills.
- The command adapter enforces the role contract.
- The browser displays a test packet through the product wrapper.
- The evaluator accepts a judge result and applies the fixed aggregation rule.
- The GEPA adapter transfers scores and permitted feedback through test doubles.
- The experiment records preserve each attempted operation and its outcome.

The pilot is a Culler preparation phase outside GEPA's candidate search.
A small pilot must use reviewed development cases and the assigned Packager and judge configurations.
The pilot must verify actual packet production, browser assessment, and score records.
The first pilot must use the five reviewed Memos cases that require no supplied application media.
Each case must receive three trials with equal provisional weights across the six quality dimensions.
The pilot must preserve its actual case manifests, hashes, and evaluated directive before the first model call.
The record must label scores from an uncalibrated Judge as exploratory.
The pilot report must show raw quality beside the contribution after contract and critical failure checks.
The report must show the observed variation between repeated trials.
All pilot case groups must remain outside the final holdout set.

Reference packets and Judge calibration remain outside readiness for the first exploratory experiment.
Later calibration must preserve independent human assessments and exclude final case groups.

An optimizer integration check must verify directive edits through the assigned Codex configuration.
The integration evidence must include a GEPA proposal and its comparison against the parent on development cases.
Pilot and integration results must remain separate from final comparison evidence.

Automatic evolution requires the complete reviewed case corpus and a fixed protocol.
The first experiment must identify its Judge as uncalibrated.
An uncalibrated experiment can produce directive candidates and final comparisons.
It must establish no automatic claim of directive improvement or qualification for source adoption.
The fixed protocol must state the case counts, trial counts, and experiment completion rule.
The final comparison must follow candidate selection and use the independent final cases.

## Packager execution

### Complete role input

The source revision must contain the agreed Packager section structure before the first trial.
For the first Packager experiment, the original directive must combine these source texts from the selected source revision:

- The Packager instructions must come from `mc/verbs/directives/packager.md`.
- The full `writing` instructions must come from `mc/verbs/skills/shared/writing/SKILL.md`.

The framework must merge both instruction bodies unchanged before the first trial.
The merged text must form one editable Packager directive.
The Packager must receive the `writing` instructions only through that directive.
The trial input must omit the separate `writing` skill and its generated pointer.
The remaining generated skill pointers must match the selected source revision.
The role brief must reproduce the relevant facts and explanations from the selected product revision.
Its parts must match that revision's Packager input.
Stored case records can retain evidence that the selected Packager brief omits.

Each trial must receive the complete applicable input:

- The candidate directive must replace only the permitted directive text.
- The role brief must contain the case facts.
- The role skills must match the selected source revision.
- The remaining shared skills must match the selected source revision.
- The Project skills must match the case manifest.

The case manifest must record an empty Project skill set when none applies.
The framework must verify input assembly against product behavior.
Those checks must account for the merged `writing` instructions and the remaining skill pointers.

The case facts and brief explanations must remain fixed across candidate comparisons.
Only the candidate text and required trial identity or artifact paths can differ.
Those differences must remain traceable to the trial manifest.
All skills supplied as separate files must remain fixed across those comparisons.

### Isolated environment

The trial must use the configured real agent harness.
Its environment must reproduce the relevant paths and permissions that the Packager expects.
Its repository must contain the actual reviewed Git commits.
Its evidence files must remain available through the role's ordinary file tools.

| Trial path | Required content |
| --- | --- |
| `/workspace/source` | An isolated repository with the case's reviewed commits and start state. |
| `/mc/run.json` | A synthetic run envelope for this trial. |
| `~/.agents/skills/` | The fixed skill files, with the separate `writing` skill absent. |
| `/mc/artifacts/` | The case's evidence paths and private output plane for the packet. |

The case manifest must define each path that its brief or skills reference.
The harness profile must record all additional instructions, tools, and skill paths that it supplies.
Unrelated host instructions, memory, and Project files must remain outside the trial environment.
The configured harness must support the subagent behavior that the complete directive requires.
Subagent traces and usage must belong to their parent trial record.

Each trial must start with fresh files and command state.
Each trial must start with a fresh harness session.
Session memory and output artifacts must remain isolated between trials.
The environment must exclude other candidates and their result records.

The evaluator must use private credentials and synthetic run identity.
The deployed Culler and its state must remain outside the trial environment.
Evaluation commands must remain confined to that environment.

The framework must retain the Packager's repository preservation rules.
The role must inspect the diff for the actual reviewed Git range.

### Supplied visual evidence

Each initial packet case must supply all application screenshots and videos that the packet requires.
The Verifier evidence must identify each media file and the state or interaction that it shows.
Each media file must identify the source revision that it represents.
The case review must verify that the supplied media accurately covers the required visual states.
The media files and their descriptions must remain fixed across candidate comparisons.

Each initial packet case must permit completion without an application build or an application instance.
The case author can obtain media from a temporary application instance before the experiment.
Later trials must reuse the recorded media without an application capture step.
The manifest must distinguish the evidence capture environment from the trial environment.

The Packager can create explanatory diagrams from the supplied facts.
The packet must distinguish those diagrams from captured application evidence.
The judge must assess the selection, presentation, and explanation of supplied media as part of packet quality.
The result report must identify application media capture as outside the evaluated scope.

### Command adapter

An evaluation command adapter must reproduce the Packager's permitted command behavior.
It must validate submitted identities and artifact locations.
It must enforce the relevant repository and terminal requirements.
It must provide the case responses for permitted read commands.
It must reject unsupported commands.

Adapter checks must compare acceptance and refusal behavior with product validation.
Those checks must use disposable evaluation state.
Any scratch database access must follow Culler's database ownership rules.

The adapter must retain every attempted command and its response.
A refusal must permit the repair that the real command contract permits.
A trial must record at most one successful terminal result.
The evaluator must distinguish a packaged result from an infrastructure report or operator request.

## Cases and datasets

### Case content

Each case must represent the task state that the Packager receives before it produces a result.
Each case must identify its repository source and license.
Its manifest must fix the reviewed commit range and required evidence files.
The repository must contain the source context necessary to explain the reviewed change.

Each case must fix these inputs where they apply:

- The task states its purpose and acceptance criteria.
- The repository contains the actual source before and after the change.
- The upstream role records describe the implementation, the latest verification gates, and their results.
- The evidence files contain captured check results and relevant media.
- The command state supplies the records and responses that the Packager can access.

The case record must distinguish captured upstream reports from reports authored for the experiment.
Claims of executed checks must link to actual execution evidence.
Authored reports must preserve material evidence limits.
The case must supply the same kinds of information as the product's Packager input.

Reference facts must derive from the case records and source behavior.
An independent case review must verify those facts before the case affects a result claim.

The reference facts must identify the behavior before and after the change.
They must identify the mechanism and material conditions that an accurate explanation preserves.
They must identify the claims that the available evidence cannot support.
The references must permit different accurate explanations and visual designs.
The judge must assess those explanations against facts, rather than require a particular reference packet.

Each case must define its expected terminal result.
Each case must identify its important behavior and evidence limits.

### Repository sources

The first Packager experiment must use [Memos](https://github.com/usememos/memos) as its sole case repository.
Its corpus must contain independent Memos changes across the seven case types below.
The source selection must prioritize verifiable behavior and distinct explanation requirements.
The corpus must include visible interface changes and internal behavior changes.
Each source must support reproducible case checks with fixed dependencies and non-sensitive data.

Cases from actual Memos changes must provide the main evidence for practical packet quality.
Agents can author additional changes to cover absent conditions.
Authored changes must exist as actual repository changes with independently verified reference facts.
The corpus must distinguish actual project changes from changes authored for the experiment.

The result claim must describe performance on the evaluated Memos changes.
The final cases must assess independent Memos changes outside the development case groups.
The case format and evaluator must support other repositories through their manifests and fixtures.

### Case storage and preparation

Case manifests and reconstruction instructions must reside under `evals/`.
External repository checkouts must reside outside the Culler checkout and its release artifacts.
The framework must obtain external source from the locations that the case manifest records.
Source acquisition must preserve the required repository history and license records.

Each case from an upstream change must identify the full base and reviewed commit identifiers.
A case with an authored change must identify a fixed base commit and an immutable patch or commit artifact.
Case preparation must reproduce the recorded Git range and verify the resulting source tree.
Release names can describe revisions, but full commit identifiers must define the source that a trial receives.

The manifest must identify the fixed dependencies and environment that the case requires.
Required reports and evidence must have recorded content hashes.
Large evidence files and source archives can reside in a separate artifact store.
Each external artifact must have a recorded location and content hash.

The framework must obtain and verify the required artifacts before a trial starts.
A local cache can reuse verified source artifacts across trials.
Every trial must receive isolated repository files and command state from the prepared case.
The trial must receive the fixed task records and evidence beside the repository.
Case preparation must report absent artifacts or content mismatches before role execution.

### Explanation coverage

The initial Memos corpus must cover these seven case types:

| Case type | Required explanation |
| --- | --- |
| A small visible change | The packet explains the effect with proportionate detail. |
| A feature across several components | The packet connects the user action to the component interactions and result. |
| A state transition or recovery change | The packet explains the event sequence and conditions that control the outcome. |
| A configuration or compatibility change | The packet explains which users or installations receive different behavior. |
| A data or access change | The packet explains which data or permissions change and any material preservation limits. |
| An internal refactor | The packet explains the structural change and identifies the behavior that remains unchanged. |
| A performance change | The packet distinguishes the mechanism from measured benefits and unverified performance claims. |

The corpus must include changes at different levels of scope and complexity.
It must include cases where a useful diagram explains relationships that concise text alone makes difficult to understand.
It must also include cases where concise text gives a sufficient explanation.

A case can cover several explanation requirements.
Each case type must contain multiple independent case groups in the complete experiment corpus.
A case that covers several types must count once in the total case count.
The coverage record must show each case type's representation in each dataset.
Case types describe explanation requirements.
Dataset assignments describe how the experiment uses cases.

### Evidence and contract coverage

The corpus must also cover these evidence and contract conditions:

- A configuration or incidental file belongs to the reviewed range.
- A Verifier record leaves a material risk or check unverified.
- The latest Verifier evidence preserves material limits after a correction.
- The latest evidence distinguishes the effective requirements from earlier source evidence.
- A packet uses relevant media that the evidence supplies.
- A required evidence file is unavailable.
- A terminal refusal permits repair and resubmission.

Evidence conditions can apply to any explanation case.
An evidence condition must remain consistent with the case records and repository state.
Cases whose expected result contains no packet must remain outside packet quality averages.
The report must show their contract results separately.

### Case independence

Related cases must share a group identifier.
Variants of one change and its related corrections must remain in the same dataset group.
Repeated trials and altered evidence for one change must not count as independent changes.

The protocol must state whether final cases assess new changes within known repositories or changes from unseen repositories.
A claim about unseen repositories must use final repositories absent from all development datasets.

### Dataset separation

The protocol must separate these datasets:

- The training set (trainset) must supply minibatches for directive edits and comparisons against each parent.
- The validation set (valset) must supply scores for GEPA parent selection and candidate comparisons during the search.
- The final holdout set must assess the selected candidate after directive edits stop.

The dataset assignments must remain fixed during the experiment.
The training set and validation set must use disjoint case groups.
The training set and validation set must exclude the final case groups.

Judge calibration and its check set must also exclude final case groups.
Rubric development must exclude final case content and results.
The optimizer's files and tools must exclude final case content and final-test feedback.
The rubric must remain available to the optimizer.

Pilot cases can come from the development case groups.
The pilot requires no fourth GEPA dataset.
Its results must remain separate from the GEPA run's score records.

| Recipient | Permitted case evidence |
| --- | --- |
| Packager | The assigned case's normal role inputs, repository, and upstream evidence. |
| Judge | The assigned case's references, rubric, and packet display. |
| Optimizer | The parent directive and permitted evidence from its training minibatch. |
| GEPA | Training scores and stored validation scores for its decisions. |
| Evaluator | All case records and execution evidence necessary for the declared protocol. |

The optimizer environment must enforce its case access through available files and tools.
Instruction text alone cannot establish the holdout boundary.
The Packager must receive the visible inputs of its assigned final case during the final comparison.
The evaluator must retain exclusive access to case references that the role cannot inspect.
The operator must receive the complete result after the comparison.

A final case becomes a development case when its result guides another directive edit.
A later independent comparison must use fresh final cases.
Published final cases can remain as public regression cases.

The protocol must fix case counts and trial counts before the search.
Its final sample must support the declared comparison precision.
Insufficient precision must produce an inconclusive result.
Repeated trials must remain distinct from independent cases in every report.

## Packet assessment

### Reader and terms

The packet must use established Project or domain terms.
Its reader uses the Project and knows its core terms.
A repository path can appear only when the explanation requires it.
The evaluator must retain evidence identities and paths in its separate records.

The packet must use one term for one concept.
Term classification must follow the selected directive:

- A core term names the Project, a main interface part, or a concept the operator meets during ordinary use.
- An established term appears in the Project before the reviewed change.
  Its code and documents can establish that use.
  Its wiki and interface text can also establish that use.
- A new term enters the Project through the reviewed change.

The prose must use core and established terms without definitions.
The prose can define a central new term.

The Glossary must include each new term that the prose does not define.
It can include an established term to refresh the operator's knowledge.
It must exclude core terms.
Each entry must identify the term and provide its definition.
The definition must convey the term's Project or domain context without an explicit origin label.

### Explanation structure

The original directive and every candidate must retain this section order:

| Position | Section | Required content |
| --- | --- | --- |
| 1 | Summary | The packet states what changes and why it matters. Its text matches the submitted summary. |
| 2 | Changes since the previous packet | The packet states the difference from the previous packet. The section applies only when the run brief has a Previous packet part. |
| 3 | Background | The packet explains the affected Project part before the change, at the depth the operator needs. |
| 4 | Intuition | A concrete example explains the central idea with proportionate detail. |
| 5 | Technical | The packet explains the actual mechanism at the appropriate level of abstraction. |
| 6 | Criterion evidence | Prose relates each acceptance criterion to the Verifier gates, their results, and their evidence limits. |
| 7 | Evidence media | Useful supplied screenshots and videos have captions that identify their meaning. |
| 8 | Risk and rollback | Claims reflect the authority and limits of the durable evidence. |
| 9 | Glossary | Each required entry defines a new term that the prose does not define. |

Required sections and omissions must follow the selected directive's applicability rules.
The Risk and rollback section must remain present.
Each glossary entry must carry an element identifier.
Each use of its term in prose must link to that identifier.
The packet must use one page with section headings and a table of contents.
Decision-relevant content must remain visible without expansion.

The Technical section must explain how the actual change works.
The Packager must select technical detail according to the change's scope and explanatory needs.
The judge must assess whether that choice supports representative understanding with low cognitive load.

A broad change can require explanation of entity relationships and interactions between subsystems.
A narrow change can require details about a component or function.
The essential explanation must remain understandable without code inspection.

The product generates Files changed independently of the Packager.
Files changed holds the complete diff and file inventory outside the packet.
The packet must explain material behavior and recorded risks from the reviewed change.
The packet must include these details only when they are necessary to understand the change:

- A code snippet
- A source path
- A commit identifier

The judge must assess whether each such detail is necessary for comprehension.

The section's purpose must guide the choice and placement of visuals.
The Packager can use the following forms where they help the explanation:

| Section | Purpose of its visuals |
| --- | --- |
| Background | A visual explains relevant entities and existing relationships. |
| Intuition | A visual explains the central idea through a concrete example or a before-and-after comparison. |
| Technical | A visual explains actual interactions at the detail level that the change requires. |
| Evidence media | Media shows recorded application behavior and identifies its source. |

The Intuition section can use a central worked example to explain the change.
The Technical section can continue that example to explain the implementation.

Visuals in other sections must serve their specific explanatory purpose.
Each visual must appear with the explanation that it supports.
The packet must distinguish explanatory diagrams from captured evidence.
Each diagram must preserve material system behavior and relevant conditions.

### Contract checks

Deterministic checks must assess the requirements that code can verify.
They must include the artifact and terminal contract.
The Judge must assess requirements that need interpretation.
Each such assessment must cite authoritative evidence.

The packet must satisfy the current self-contained Hypertext Markup Language (HTML) contract.
Required media must remain embedded in the packet.
The packet must preserve the selected product revision's resource, script, and size restrictions.

The evaluator must use Culler's packet wrapper and iframe restrictions.
The evaluator must retain rendered evidence at desktop widths.
The protocol must identify the browser version and viewport dimensions.

The initial display checks must use a width of 1280 pixels.
They must expose unreadable text and content that the display blocks.
They must expose clipped content and failed embedded media.

A required infrastructure report must receive a contract assessment without a packet quality score.
Separate contract trials must require only the fixed source revision, Packager runtime, external retry rule, and reviewed cases.
Their trial count must remain explicit.
They must require no Judge, quality rubric, calibration, or Optimizer.
The evaluator must classify failures from independently captured evidence.
A role-caused absent required packet must receive zero quality credit and a contract failure.
An unsupported failure report must count as a role failure.

An evaluator or provider failure must follow the protocol's paired retry rule.
An unresolved external failure must leave the comparison incomplete.
The report must show every case and trial in the applicable denominator.

### Browser inspection

The judge must inspect the packet through a real desktop browser.
The browser must use the product's packet wrapper and display restrictions.
The judge must receive screenshots and tools for ordinary packet interactions.
Those tools must support these actions:

- The model can scroll through the packet.
- The model can follow internal section and glossary links.
- The model can expand permitted collapsed content.
- The model can operate native media controls when media is present.

Each assessment must start from the recorded viewport and default packet state.
The judge must inspect each required section before it assigns a complete packet score.
The assessment must identify content that the model could not inspect.
The evaluator must retain the screenshots and interaction trace for each assessment.

Text from visible packet content can supplement screenshot inspection.
The judge's inputs must exclude raw packet HTML and hidden content.
Deterministic contract checks can inspect raw packet HTML and browser diagnostics.
Content present only in source markup must receive no credit for comprehension or visual explanation.

The judge must receive the authoritative case evidence and rubric alongside the rendered packet.
The judge's filesystem tools must exclude the raw packet file.
Its browser tools must preserve the restriction to visible packet content and permitted interactions.

### Judge result

Each judge assessment must produce a structured result with these fields:

- The assessment identifier links to the trial and case reference version.
- Each rubric dimension has a score, explanation, and evidence references.
- Each critical failure has its category and supporting evidence.
- Semantic contract checks identify their outcomes and reasons.
- The result identifies uninspected or inaccessible packet content.
- Diagnostic feedback identifies specific explanation defects and useful improvements.
- Artifact references identify the screenshots and browser interactions that support the assessment.

The evaluator must validate the result structure and score ranges.
The judge must perform comparisons that require interpretation of natural language.
The evaluator must preserve that judgment and compute the declared aggregates.
Claude Code packet assessments and edit scope assessments must use structured output with an explicit JSON Schema.
The schema must require the result fields, field types, and score ranges.
The packet schema must require each semantic check exactly once.
The runtime must consume the validated structured result instead of the final prose.
It must preserve the native result and the schema identity with the assessment evidence.
The existing checks for evidence, inspection, and score validity must also pass.
An absent validated result must follow the assessment retry policy.
An exhausted structured output retry must remain an infrastructure failure.
It must remain distinct from a Packager failure.

### Case score aggregation

Each evaluation must identify its directive, case, stage, and required trial count.
Every trial must preserve its packet, contract result, rubric scores, and feedback.
The evaluator must compute the weighted quality score from the judge's dimension scores.
A trial with a contract failure or critical failure must contribute zero to GEPA.
The record must preserve the original quality assessment beside that zero contribution.
Successful command use must supply no quality bonus.

The final comparison must use the same trial contribution rule.
Gain thresholds must use the displayed scale from zero to 100.

The evaluator must average the trial contributions within each case.
The GEPA adapter must divide that mean by 100 and return one case score from zero to one.
The initial protocol must use equal case weights within each GEPA comparison.
For example, trial contributions of 59 and 61 produce a case mean of 60 and a GEPA score of 0.60.
These values illustrate the arithmetic, not a required trial count.

GEPA must receive one score for each requested case, in the corresponding case order.
The evaluator must preserve individual trial evidence after aggregation.
Cases that expect no packet must receive separate contract checks outside GEPA's packet quality datasets.
An unresolved evaluator or provider failure must prevent a completed comparison score.

### Quality rubric

The protocol must fix the operator's assumed technical knowledge and Project familiarity for the experiment.
The operator understands general software concepts and uses the Project.
The operator knows its core terms.
The packet must explain the affected Project structure and new terms at the necessary depth.
The packet must use clear technical language without unnecessary jargon or complexity.
The judge must use those assumptions throughout candidate comparisons.
Those assumptions define assessment context and carry no separate quality score.
The protocol must distinguish required content from presentation requirements.
Candidate directives must receive quality scores from the packets that they produce.

The quality rubric must assess only the packet that the Packager produces.
Files changed and its generated Git data must remain outside the quality rubric.
The case repository and its diff must supply reference evidence for packet claims.
The judge must assess the accuracy and explanatory value of any code or Git detail that the Packager includes.

Visual explanation must use an anchored scale from zero to five.
Each other quality dimension must use an anchored scale from zero to four.
Each dimension must declare its maximum in the fixed rubric.
The score calculation must divide each dimension score by its declared maximum.
The weighted mean of those normalized scores, multiplied by 100, must produce the quality score out of 100.

| Dimension | A strong result |
| --- | --- |
| Comprehension | The packet gives a representative mental model of the changed behavior and material conditions. |
| Causal explanation | The packet explains how the mechanism produces the result at an appropriate level. |
| Decision support | The packet faithfully represents verification evidence, uncertainty, reported risks, and rollback limits. |
| Visual explanation | The actual desktop display makes relevant relationships easy to inspect and understand. |
| Clarity | The packet uses plain technical language, consistent established terms, and logical conceptual order. |
| Packet length | The amount of content matches the change's complexity and the operator's needs. Each part justifies its review effort. |

Each dimension must define an anchor for each score level in its range.
The protocol must define dimension weights before the search.
The first exploratory experiment must use equal weights across all six dimensions.
The experiment must preserve its rubric and weights throughout candidate comparisons.

The judge must inspect authoritative case evidence and the actual packet.
Visual assessments must include the rendered packet.
Each judgment must cite the packet content and evidence that support its score.

The rubric must reward useful explanation and proportionate length.
Additional text and figures must earn credit through their contribution to comprehension.
A simple change can earn four for visual explanation when an additional figure supplies no useful information.
Candidates can vary visual design and explanation techniques within the packet contract.
The rubric must permit different effective designs to earn the highest score.

Risk claims must remain within the authority of the durable Verifier evidence.
The judge must recognize an explicit evidence limit as a valid explanation.

The packet can omit implementation details that do not affect its explanation of the change.
A representative explanation must preserve the material behavior and causal relationships.
The packet must preserve relevant conditions and evidence limits.
The judge must distinguish harmless omission of detail from a misleading explanation.

These critical failures must remain separate from the weighted score:

- A material claim contradicts the authoritative evidence.
- An omitted condition creates a materially incorrect impression of system behavior.
- The packet invents a successful check or an unsupported risk conclusion.
- The packet omits a material behavior change or evidence limit.
- The packet gives an incorrect causal explanation that can change the operator's decision.
- A resource or permission violation breaks the packet contract.

Every critical failure must identify its evidence.
A quality average cannot compensate for a critical failure.

### Comprehension

The judge must assess whether the packet gives the operator a representative mental model of the change.
Comprehension assesses the mental model that the packet teaches.
Clarity assesses whether the packet's language and structure are easy to follow.
The judge must explain each score with evidence relevant to that dimension.
The assessment must use the packet explanations through text, visuals, and permitted interactions.
Case evidence must check accuracy without supplying explanations that the packet omits.
The judge must assess these aspects of the explanation:

- The packet identifies the changed behavior.
- The packet identifies the scope and practical effect of the change.
- The packet explains material conditions and edge cases.

| Score | Required outcome |
| --- | --- |
| 0 | The packet fails to explain the change or creates a materially incorrect mental model. |
| 1 | The packet names the change, but its practical effect remains unclear. |
| 2 | The packet explains the main effect, but important scope or conditions remain unclear. |
| 3 | The packet explains the change and material conditions with minor gaps that preserve the mental model. |
| 4 | The packet gives a representative mental model. The operator can explain the change and predict the relevant outcomes from the packet alone. |

Useful abstractions can earn the highest score.
The packet can omit internal details that do not affect the representative mental model.

The judge must identify essential explanations that the packet omits.
The judge must use reference evidence to check accuracy.
The judge must give comprehension credit only for explanations present in the packet.
The judge's feedback must cite packet content that supports each comprehension score.
The comprehension score must represent the judge's assessment of likely operator comprehension.
The result report must distinguish this assessment from observed human comprehension.

### Causal explanation

The judge must assess whether the packet explains how the mechanism produces the result.
The assessment must include structural relationships when the change concerns internal structure.
The judge must give credit only for causal relationships that the packet explains.
The judge must distinguish essential causal relationships from internal details that the operator does not need.

| Score | Required outcome |
| --- | --- |
| 0 | The packet gives a materially incorrect cause or identifies no relevant mechanism. |
| 1 | The packet names a relevant mechanism but does not connect it to the result. |
| 2 | The packet explains part of that connection, but leaves an important causal relationship unexplained. |
| 3 | The packet explains the main causal relationship accurately. Some useful connections remain implicit. |
| 4 | The packet explains the essential causal relationships at an appropriate level. The operator can explain why the result follows from the packet alone. |

Useful abstractions and concise explanations can earn the highest score.
The judge must reward examples and diagrams through their explanatory value.
The highest score requires neither an example nor a diagram.
An incorrect cause that can change the operator's decision must also trigger a critical failure.

### Decision support

The judge must assess whether the packet faithfully represents the evidence that informs the operator's decision.
Verification coverage concerns what the recorded checks establish.
The packet must distinguish established claims from uncertainty.
The Packager must represent durable Verifier evidence without a new verification or risk assessment.

| Score | Required outcome |
| --- | --- |
| 0 | The packet materially misstates verification, risks, or rollback, or provides no useful account of the evidence. |
| 1 | The packet reports a verdict or generic assurance, but leaves its basis and limits unexplained. |
| 2 | The packet identifies the main evidence, but leaves an important uncertainty, risk, or rollback limit unclear. |
| 3 | The packet faithfully represents the evidence and its limits. Minor omissions preserve the basis for the operator's decision. |
| 4 | The packet faithfully represents all decision-relevant evidence, verification coverage, uncertainty, reported risks, and rollback limits. |

Limited verification can earn the highest score when the packet faithfully represents the evidence limits.
The judge must assess the packet's evidence account separately from the strength of the verification itself.
An invented successful check or an unsupported risk conclusion must also trigger a critical failure.

### Visual explanation

The judge must assess how the actual desktop display helps explain the change.
The assessment must include the layout and displayed media.
The judge must assess whether captions and visual relationships support an accurate explanation.
Clarity assesses the language and conceptual order.

| Score | Required outcome |
| --- | --- |
| 0 | A visual creates a materially incorrect impression, or the display makes essential content unusable. |
| 1 | Clutter, illegible content, or confusing visual relationships substantially obstruct the explanation. |
| 2 | The display clarifies some relevant relationships, but significant visual or layout defects increase effort. |
| 3 | The display presents relevant relationships accurately and readably. Minor visual or layout defects create avoidable effort. |
| 4 | The display makes relevant relationships easy to inspect and understand, without unnecessary visual effort. |
| 5 | Original visuals convey the essential model, mechanism, and material conditions accurately. The operator can understand these from the visuals with little prose. |

Concise text can earn four when a figure adds no explanatory value.
Five must reward visuals that make the essential model intuitive before the operator reads the detailed prose.
The visuals must convey the relevant relationships, changes, and conditions at an appropriate abstraction.
A diagram tailored to the change can earn five with simple shapes and labels.
Prose can supply supporting detail.
Novelty, decoration, visual complexity, and figure count must earn no credit by themselves.
Creative designs must earn credit through their contribution to comprehension.
A materially incorrect visual impression must also trigger the applicable critical failure.

### Clarity

The judge must assess how easily the operator follows the packet's language and conceptual order.
The assessment must use the experiment's fixed assumptions about the operator's technical knowledge and Project familiarity.

| Score | Required outcome |
| --- | --- |
| 0 | The language or conceptual order prevents a useful interpretation of the explanation. |
| 1 | Major problems with language, terms, or conceptual order require substantial effort to follow the explanation. |
| 2 | The explanation is understandable, but undefined terms, inconsistent names, vague references, or conceptual jumps cause confusion. |
| 3 | The packet uses clear technical language, consistent terms, and logical conceptual order. Minor ambiguities create avoidable effort. |
| 4 | The packet uses plain technical language and consistent established terms. It explains new terms where needed. Concepts follow a logical order. |

Abbreviations must appear in full at first use.
When the packet names a responsible entity, it must use the abstraction appropriate to the explanation.
Technical depth can earn the highest score when the change needs it.
Clear language must preserve the explanation's meaning.

The judge must apply the selected directive's core, established, and new term rules.
The glossary contract checks whether required entries exist.
Clarity assesses whether the definitions help the operator understand the terms.

### Packet length

The judge must assess packet length relative to the change's complexity and the operator's need for an accurate explanation.
Repetition and unnecessary detail must reduce the packet length score when they increase operator effort.
The judge must assess the complete display, including figures and permitted interactions.

| Score | Required outcome |
| --- | --- |
| 0 | Excess content prevents useful review. |
| 1 | Substantial unnecessary detail or repetition requires extensive effort to find the essential explanation. |
| 2 | Unnecessary detail or repetition creates noticeable effort across the packet. |
| 3 | The content is mostly proportionate. Minor unnecessary detail or repetition creates avoidable effort. |
| 4 | The amount of content matches the change's complexity and the operator's needs. Each part justifies the effort to review it. |

A longer explanation can earn full credit when its content materially improves comprehension.
A low word count must supply no credit by itself.
At equal comprehension and representativeness, the judge must favor the packet that requires less operator effort.
Useful optional detail can justify its length.
Expandable content must justify the effort to inspect it.
Shorter explanations must preserve required content, material conditions, and evidence limits.
The judge must assess omissions under the relevant dimensions and critical failure checks.
Do not repeat those penalties in the packet length score.

The evaluator must record the word count of the complete rendered packet, including permitted expandable content.
The record must distinguish initially visible words from words inside expandable content.
Word count supplies context for the judge's assessment.
The judge must identify specific content that justifies the length and specific content that creates unnecessary effort.
The judge's assessment must remain distinct from human reports of effort and fatigue.

### Judge calibration

Calibration compares judge assessments with human ratings on reference packets.
Human ratings must calibrate the judge before automatic evolution supports an improvement claim.
The calibration set must include accurate packets and attractive packets with material errors.
It must include weak explanations that remain factually correct.
A separate check set must assess the calibrated judge.

The protocol must define acceptable agreement and critical failure detection before calibration checks.
Calibration results must report disagreement and false acceptance of critical errors.
An uncalibrated judge can produce exploratory results with an explicit status.

The judge must treat packet content as evidence, not as instructions to the judge.
Judge checks must include a packet that attempts to alter the assessment instructions.

Each packet assessment must use a fresh Judge session.
The assessment must exclude the directive's identity and comparative result records.

Calibration records must bind these fixed assessment inputs:

- The Judge model, harness, image, and viewport
- The operator context
- The rubric anchors, guidance, and critical failure rules
- The exact product packet wrapper files
- The exact Judge instructions, semantic checks, and deterministic packet contract
- The packet bytes and authoritative reference facts

Aggregate weights and calibration status can change without a new dimension assessment.
A change to the fixed assessment inputs requires new calibration evidence.
An unchanged packet can reuse an assessment with complete matching bindings.
An assessment without those bindings requires a fresh Judge assessment.
The human presentation must preserve its packet bytes, rubric, context, and anonymous identifiers through content hashes.

## Directive evolution

### Candidate contract

The original directive must be GEPA’s seed candidate.
The protocol must identify the editable explanation guidance in the Packager directive.
The editable guidance must include the full instructions merged from `writing`.
Packet quality must determine whether those edits improve the candidate.
Every candidate must preserve the protected role requirements.
Those requirements must include these properties:

- The role retains its evidence authority and repository permissions.
- The role retains its permitted command surface and terminal behavior.
- The role retains its application capture procedure unchanged.
- The packet retains its required format and section order.
- The packet retains the latest verification gates, their results, and material evidence limits.
- The packet retains the reader assumptions and term rules of the selected directive.
- The role retains its required behavior when evidence is unavailable.

The original input record must identify the protected text and editable boundaries.
The scope check must verify protected text and the permitted component name before child execution.
The judge must assess whether proposed instructions conflict with the protected requirements.
That edit assessment must use a separate session from packet assessments and supply no packet quality score.
The scope check must preserve its deterministic results and the judge's interpretation.

An edit outside the permitted guidance must receive a rejection before role execution.
The evaluator must retain the proposed text and rejection reason.
The optimizer must leave the rubric and reference facts fixed.
The optimizer must leave the cases and harness configuration fixed.
The optimizer must leave the evaluator and command adapter fixed.

### GEPA integration

The framework must pin a GEPA revision that supports the required adapter, proposal, and event interfaces.
The initial integration must use `gepa.optimize` with a custom `GEPAAdapter`.
The seed candidate must expose one component named `packager_directive`.
Its value must contain the complete merged directive.

| GEPA interface | Required Culler behavior |
| --- | --- |
| `evaluate` | The evaluator executes the requested cases and returns case scores with artifact references. |
| `make_reflective_dataset` | The adapter prepares parent trial evidence and the judge's diagnostic feedback for the optimizer. |
| `propose_new_texts` or `custom_candidate_proposer` | The integration invokes the configured Codex optimizer and returns the complete proposed component text. |
| Callbacks and saved state | The integration preserves native decisions and state beside Culler's complete records. |

The adapter must retain parent trajectories when GEPA requests reflection evidence.
It must preserve packet assessment records during validation even when GEPA requests no reflection trajectory.
The adapter must preserve the order and identity of every case in its result.
These interfaces follow the [GEPA adapter contract](https://gepa-ai.github.io/gepa/api/core/GEPAAdapter/).

The optimizer must receive the parent directive, rubric, permitted case evidence, and judge feedback.
Each proposal must use its own optimizer session with the fixed configuration.
The evaluator must preserve the exact optimizer input, output, and extracted candidate text.
The optimizer must leave protocol decisions to GEPA and the evaluator.
A scope rejection must reach the experiment record without a child trial or fabricated child score.
An unchanged directive must receive a no-change outcome rather than count as a new candidate.

### GEPA configuration

The first release must provide this evolution behavior:

| Setting | Required behavior |
| --- | --- |
| Parent selection | `candidate_selection_strategy="pareto"` uses the native per-example selection rules. |
| Frontier | `frontier_type="instance"` uses each validation case as a frontier key. |
| Proposal tasks | `IndependentSampling(n)` supports a configurable number of independent proposal tasks per iteration. |
| Acceptance criterion | `strict_improvement` requires a higher child total on the parent's exact training minibatch. |
| Proposal selection | `AllImprovements` retains proposals that pass acceptance, subject to native duplicate handling. |
| Validation | `val_evaluation_policy="full_eval"` assesses the seed and accepted children on the complete validation set. |
| Mutation | Reflective mutation edits the directive. The initial configuration uses `use_merge=false`. |
| Evaluation cache | `cache_evaluation=false` keeps new evaluation requests distinct from stored comparison results. |

The parameter `n` is the number of proposal tasks per iteration.
It is not a case count, trial count, or required number of distinct parents.
The protocol must record the minibatch sampler, random seed, and perfect-score skip policy.
The selected GEPA revision must support the [optimizer configuration](https://gepa-ai.github.io/gepa/api/core/optimize/) above.

Several proposal tasks can select the same parent.
Their minibatches can differ or overlap.
Every child must use the same minibatch and trial count as its own parent comparison.
The iteration must select its proposal tasks from one recorded candidate pool state.
These requirements use GEPA's [parallel proposal interfaces](https://gepa-ai.github.io/gepa/guides/parallel-proposals/).

### Chronological sequence

Each experiment must preserve this order of dependencies:

| Order | Responsible component | Required result |
| --- | --- | --- |
| 1 | Evaluator | Reviewed cases and runtime evidence establish readiness. The experiment protocol becomes fixed with an explicit Judge status. |
| 2 | GEPA through the evaluator | The original directive receives full validation scores and enters the candidate pool. |
| 3 | GEPA | Each proposal task receives a parent and training minibatch from the current state. |
| 4 | Evaluator and judge | Parent trials produce packets, case scores, and diagnostic feedback on that minibatch. |
| 5 | Optimizer | The parent evidence supports a proposed directive edit. |
| 6 | Evaluator and judge | The scope check permits the edit or records a rejection before child execution. |
| 7 | Evaluator and judge | Permitted children receive trials on their respective parents' minibatches. |
| 8 | GEPA | The acceptance criterion compares stored parent and child scores. |
| 9 | GEPA through the evaluator | Accepted children receive full validation scores. |
| 10 | GEPA | The candidate pool and parent selection state reflect the new validation records. |
| 11 | Evaluator | The declared completion rule permits another iteration or ends the search. |
| 12 | GEPA | The completed run returns the candidate with the highest stored validation mean. |
| 13 | Evaluator | A changed candidate remains fixed while fresh trials compare it with the original on final cases. |
| 14 | Evaluator and human reviewer | The final decision applies the declared evidence requirements. Source adoption remains a separate change. |

Orders 3 through 11 form one iteration.
Independent operations within an iteration can overlap when their inputs are complete.
The record must preserve actual start times, completion times, and dependencies.
The native evaluation order follows GEPA's [reflective proposer](https://github.com/gepa-ai/gepa/blob/main/src/gepa/proposer/reflective_mutation/reflective_mutation.py).
Validation and pool updates follow the [GEPA engine](https://github.com/gepa-ai/gepa/blob/main/src/gepa/core/engine.py).

Parent selection must use stored validation scores without a new Packager trial.
The later parent trials must obtain diagnostic evidence from the selected training minibatch.
The child comparison must reuse those parent scores.
Its acceptance decision must create no additional trial.
Strict improvement concerns the matched minibatch total, not dominance across all validation cases.
An equal total must fail the configured [acceptance criterion](https://gepa-ai.github.io/gepa/guides/acceptance-criterion/).

A rejected child must receive no validation score.
An accepted child can have a lower validation mean than its parent.
Older candidates must retain their recorded validation scores when a new child enters the pool.
The framework must distinguish stored candidate history from eligibility for parent selection.
Native skips and duplicate proposals must retain their reasons and actual trial counts.

### Parent selection and returned candidate

GEPA's per-example frontier tracks leaders on individual validation cases.
The parent selection rule weights eligible candidates by their retained case leads.
The framework must preserve the pinned library's rules for ties and redundant candidates.
It must record the resulting probabilities and chosen parent for each proposal task.
The implementation must use the native [candidate selection rules](https://gepa-ai.github.io/gepa/guides/candidate-selection/).

A general Pareto frontier contains candidates that no other candidate dominates.
Dominance means equal or higher scores on every case and a higher score on at least one case.
A candidate can be undominated yet lead no individual validation case.
Such a candidate can remain stored with zero parent selection probability under this configuration.
The highest validation mean determines the returned candidate independently of that probability.

### Completion and optional confirmation

The search must support repeated rounds and complete records for each proposal batch.
Automatic evolution must operate unattended after the cases and judge satisfy their readiness requirements.
The protocol must define the evidence that completes a search.
Repeated rounds without useful improvement can satisfy that completion rule.
The rule must identify its score source, minimum gain, and counter behavior.
The result must identify the exact condition that ended the GEPA run.

The protocol can include a fresh confirmation comparison on validation cases.
If enabled, that comparison must use fresh trials for the apparent best and its declared reference directive.
Confirmation results must retain separate records from GEPA's stored validation scores.
The completion rule must state how confirmation changes its counters.
Confirmation uses development cases and supplies no independent final evidence.
The framework must record whether the experiment enables confirmation.

Search retention must remain separate from final-test eligibility.
Final-test eligibility must remain separate from source adoption and deployment.
An experiment can retain the original directive as its result.
When GEPA returns the original directive, the experiment must retain it and report no evolved candidate.
The final comparison must then record a declared skip.

## Controlled comparison

### Original directive

The unchanged original directive is the comparison control.
The final comparison must evaluate it against the candidate that GEPA returns.
Both directives must use the same remaining skills and execution configuration.

The comparison measures improvement against the original directive with its merged `writing` instructions.

The final comparison must execute fresh trials for each directive.
The execution order must distribute service variation across directives.
Saved outputs can support calibration but cannot count as fresh role trials.
A reused result must retain its original trial identifier.
Each trial must contribute at most once to a comparison sample.

### Variation and uncertainty

Each directive must receive the declared trials on every assigned case.
The protocol must apply the same trial count and retry policy to compared directives.
It must distinguish role failures from evaluator or provider failures.
Every failed attempt must remain in the record.
Role failures must count in the comparison even when a later attempt succeeds.
Additional diagnostic attempts must remain outside the predefined comparison sample.

The analysis must compare scores within each shared case.
It must preserve repeated trials within their case groups.
It must estimate uncertainty from independent case groups.
The report must state how it assigns case groups and estimates uncertainty.

The report must show the aggregate score and each rubric dimension.
It must also show critical failures and important case-type results.
The aggregation weights must remain fixed across candidate comparisons.

### Final decision

The search must select one candidate before the final comparison.
The selected directive and final decision rule must remain fixed for that comparison.
The protocol must define a minimum useful gain and acceptable case-type regressions before the search.
Human calibration and observed variation must support those values.

A candidate qualifies for source adoption only when all these conditions hold:

- The experiment satisfies its record completeness checks.
- The judge satisfies its calibration criteria.
- The candidate passes every applicable contract check in the final comparison.
- The candidate records zero critical failures in those final trials.
- The gain against the original reaches the minimum useful gain.
- The lower bound of the 95 percent uncertainty interval for that gain exceeds zero.
- Each important case type satisfies its predefined regression rule.
- A human comparison without directive labels confirms useful packet improvement.

The human comparison must include every final packet pair exactly once.
Each pair must present the original packet and candidate packet as A and B in a random order.
Each preference must identify A, B, a tie, or insufficient evidence, with a reason.
The review must identify the human reviewer and confirm the absence of directive labels.
The review must bind its results to the exact final trials and packet content hashes.
The reviewer must give a reason for the overall comparison.

The strict pair majority rule must require more than half the pairs to favor the candidate.
Ties and insufficient evidence must count against confirmation.
A strict majority for the original must classify the candidate as worse.
The human result must complement the automated qualification requirements.
An absent human review must produce an explicit unresolved final decision.

The framework must report an inconclusive result when the evidence cannot support a decision.
An inconclusive result must retain the original product directive and all experimental candidate directives.
The report must distinguish zero observed critical failures from a guarantee about future trials.

## Complete experiment records

### Event capture

GEPA callbacks must supply native events where the pinned library exposes them.
The evaluator must add records for scope rejections, contract checks, confirmation, and the final decision.
Harness wrappers must preserve model requests and responses even when native callback fields omit them.
Each proposal task must have its own identifier within its iteration.
An iteration identifier alone must not identify several proposals as one operation.

The event record must include these fields where they apply:

- The experiment, iteration, proposal task, candidate, and parent identifiers establish ownership.
- The stage and dataset identify the purpose of the operation.
- Case and trial identifiers link the scores to their evidence.
- Start times, completion times, and predecessor identifiers establish chronology.
- An outcome identifies success, rejection, failure, interruption, or a declared skip.
- Evidence references identify the exact records that support a decision.

GEPA can suppress exceptions from observational callbacks.
The evaluator must enforce required record writes independently of callback exception propagation.
A failed required write must prevent dependent work and qualification.
The [GEPA callback contract](https://gepa-ai.github.io/gepa/guides/callbacks/) defines the native observation hooks.

### Identity and configuration

Every experiment must have a stable identifier and a protocol version.
Every candidate must have a stable identifier, including candidates that fail before a trial.
Every trial and proposal batch must have a stable identifier.

The records must preserve enough information to reconstruct the comparison:

- The source revision and directive content must remain available.
- The complete role input and case references must have content hashes.
- The dependency and harness versions must remain available.
- The effective model configurations must remain available.
- The dataset assignments and reference versions must remain available.
- The rubric and comparison rules must remain available.

### Proposals and decisions

Each candidate record must preserve the complete directive and its exact diff.
It must identify every parent candidate.
It must preserve the proposal request and returned proposal text.
It must preserve the optimizer's stated hypothesis.
That hypothesis must identify an observed failure or useful improvement.

Each decision must identify its stage and rule version.
It must identify the actual evidence and values that produced the decision.
The optimizer's hypothesis must remain separate from the evaluator's decision basis.
An explanation written after the decision cannot replace that basis.

The records must retain rejected candidates and interrupted operations.
The records must retain errors that occur before a candidate enters the accepted set.
Each batch must show all its proposals and their individual outcomes.
The record must distinguish a scope rejection from a minibatch rejection, duplicate proposal, and infrastructure failure.
An unevaluated proposal must retain absent scores rather than fabricated zero scores.

### Trial artifacts

Every trial must preserve these artifacts when they apply:

- The complete role output must remain available.
- The command requests and observed responses must remain available.
- The packet and its display evidence must remain available.
- The contract checks and individual rubric scores must remain available.
- The judge input and returned judgment must remain available.
- The judge's feedback and evidence references must remain available.
- The duration and available usage data must remain available.

The record must identify the reason for each absent expected artifact.
The evaluator must protect credentials from inclusion in records or public artifacts.

The resident must retain all provider credentials in the host credential store.
Each evaluation session must receive separate Culler provider access for its exact model.
The access must expire within 1800 seconds and permit at most 100 provider requests.
The host bridge must read the request count and revoke the access after each session.
The bridge must revoke all remaining access when its pool closes.
The evaluation container must receive only the Culler token, proxy address, and public certificate.
Raw provider credential profiles must fail with a connection migration instruction.
The runtime must preserve its explicit effort, fast mode, and output format controls.
Historical records must remain readable without provider access.

Trial counts must remain separate from case counts and model call counts.
Usage records must distinguish Packager, judge, and optimizer calls.
Stored parent scores must link to their original trials whenever a later decision reuses them.

### Repository retention

A completed packet assessment must preserve its score, artifacts, and case manifest before repository disposal.
Disposal must affect only the two verified repository copies owned by that trial.
The source cache, source checkout, worktrees, and fixed evidence files must remain intact.

A fresh isolated check must use the recorded evaluation image without credentials or model calls.
Each copy must preserve its recorded commit, branch, source files, and Git metadata.
The check must detect unexpected ignored files.
The record must bind each disposal start to its exact verification evidence.

Incomplete trials must retain their repository copies for continuation.
Changed or unverifiable copies must remain available for analysis.
Continuation must finish an interrupted disposal without a new Packager or Judge trial.
A partial removal must retain its directory identity and prior verification evidence.
The complete case manifest and reconstruction inputs must remain available.

### Completeness and continuation

The evaluator must durably register each expected operation before that operation starts.
A record-write failure must stop the affected operation.
Each registered operation must end with a recorded outcome:

- A completed operation must retain its result.
- A failed operation must retain its failure evidence.
- An interrupted operation must retain its last known state.

Successful framework execution alone cannot establish record completeness.

Every attempt must have a complete outcome record.
The evaluator must preserve all assessment artifacts for every trial that supports qualification.
Failed or interrupted operations must preserve available artifacts and explicit absence reasons.
An unexplained record gap must block qualification.

An interrupted experiment must retain its completed evidence.
An exhausted Judge retry must preserve the completed Packager packet for continuation.
Ordinary continuation must reuse completed trial scores, including zero scores from valid role failures.
Continuation must reuse completed Optimizer proposals and scope decisions.
A retry must retain a distinct attempt record and preserve the original sample identity.
A continued experiment must preserve its identifiers and prior decisions.
Uncertain operations must remain explicit rather than count as successful work.
Continuation must restore the GEPA state and reconcile it with the evaluator's completed operation records.
The restored protocol must match the recorded cases, rubric, model assignments, and candidate texts.
An uncertain model attempt must retain its record when the retry policy permits a replacement attempt.

Machine-readable records must have a documented format and version.
The framework must preserve raw artifacts independently of a particular viewer.

## Experiment views

### Browser controls

The local browser must support experiment configuration and an explicit Start experiment action.
The browser server must invoke no model before that action.
The server must bind to the host loopback address and require its private browser address.
Requests that change state must require the server's exact origin.
The browser must receive public parameters only.
The host must retain credential paths and values outside browser responses.

The browser must expose these experiment parameters:

- Case selection within the assigned training, validation, and final holdout sets.
- Trial counts for each phase.
- Proposal count, minibatch size, concurrency, and random seed.
- The minibatch sampler and perfect-score skip policy.
- Iteration and stagnation completion rules.
- Exact model identifiers, harness settings, and the Judge viewport.
- Confirmation comparisons and retry rules.
- Final comparison thresholds and uncertainty requirements.

The browser must explain each parameter with established experiment terms.
It must distinguish cases, trials, proposals, and iterations.
The browser must provide three saved presets:

- Production.
- Production — 5 iterations.
- Test.

Production must preserve the complete experiment parameters.
Production — 5 iterations must differ from Production only in its maximum iteration count.
Its maximum iteration count must equal five.
Its description must distinguish fewer proposals from less evidence per candidate.
Test must use the parameters below:

| Parameter | Test value |
| --- | --- |
| Training cases | Seven, with one case for each change type |
| Validation cases | Seven, with one case for each change type |
| Final cases | All nine production final cases |
| Trials per case in each phase | One |
| Proposals per iteration | One |
| Training cases per minibatch | Two |
| Maximum iterations | Two |
| Iterations without the required gain | Two |
| Concurrent operations | Four |

All presets must preserve the models, harnesses, `high` effort, retry rules, and final decision rules.
The Test description must distinguish execution evidence from evidence of directive improvement.
Preset selection must invoke no model.
Preset selection must apply all parameters and selected cases together.
Manual edits must show Custom when the parameters differ from all saved presets.
The case restore action must use the last selected preset.
The available case catalog must remain complete after a preset selection.
The preset selector must lock with the other controls during an experiment.
The `serve --preset test` command must open the Test preset before any experiment starts.
The `serve --preset production-short` command must open Production — 5 iterations before any experiment starts.
The existing browser command must retain Production as its default.
Each experiment must record its exact parameters, preset identity, and parameter hash.

The browser must show the selected dataset counts before the start action.
It must show configuration errors beside the controls without discarding the current values.
The server must validate readiness before an experiment starts.
Each case must remain in its assigned dataset to preserve the final holdout.
Case selection must preserve independent groups across the three datasets.
The server must permit one active experiment at a time.
Repeated start requests must create no duplicate experiment.

Each experiment must receive a separate output directory and a fixed protocol.
Configuration edits must apply to a new experiment.
The browser must retain the fixed configuration during an active experiment.
The browser must distinguish startup, active execution, completion, failure, and interruption.
The browser must show the actual durable records after the experiment starts.
The browser must preserve inspection of those records after completion or failure.
Closing the browser must preserve active execution.
Closing the browser server must interrupt active execution and preserve experiment records.
An interrupted experiment must support continuation through the recorded protocol and the existing run command.
Portable exports and historical viewer commands must remain read-only.

### Chronology and evidence

The experiment view must fill the available viewport below compact navigation.
The view must use one experiment heading.
The current phase must reflect the latest explicit phase in the journal.
Native callbacks without a phase must preserve the latest explicit phase.
The selected event must have a separate label from the current phase.
The chronology and candidate ancestry must form the first inspection area.
The original candidate must appear without page scroll at a desktop viewport of 1440 by 900 pixels.
Experiment setup, diagnostic details, and complete score tables must remain available through expandable sections.
The Latest action must resume automatic updates in a live view.
Explicit history inspection must preserve the selected event while new events arrive.
Display updates must preserve the fixed experiment inputs and active execution.

The developer must have a chronological view of each experiment.
It must show proposal batches and their accepted or rejected candidates.
Each candidate must link to its directive diff and decision evidence.
The view must identify the current phase, iteration, operation, and responsible component.
It must distinguish Culler preparation and final comparison from native GEPA operations.

A developer must be able to inspect the experiment state at each recorded event.
That state must include only the scores and decisions available at that event.
Concurrent operations must retain their actual time intervals and dependencies.
Display order alone must imply no order between overlapping trials.

Accepted and rejected candidates must have separate graph views.
Each graph must retain parent identifiers and candidate identifiers.
Each parent identifier must link to the parent's record, including parents in the other graph.
The developer must be able to inspect rejected candidates as fully as accepted candidates.

Each node must show its candidate identifier, status, validation mean, and parent selection probability.
A companion matrix must show each candidate's per-case validation scores.
Each score must identify its dataset, trial count, and originating event.
Absent evaluations must display an absent value and its reason.
The view must show ancestry when the developer selects a candidate.

The views must distinguish these states:

- A candidate qualifies for further search.
- A candidate awaits the final comparison.
- A candidate passes the final comparison.
- A candidate fails the final comparison.
- A candidate remains inconclusive after the final comparison.
- A recorded source revision adopts a candidate.
- A recorded deployment revision uses a candidate.

The views must identify the case subset behind each score.
They must expose incomplete trials and critical failures beside aggregate scores.
They must derive displayed decisions from the stored records.
Training minibatch scores, validation scores, confirmation scores, and final scores must have distinct labels and records.

Earlier parent evaluations must link directly from the child comparison that uses them.
The view must show the matched cases and comparison rule beside each acceptance decision.
Detailed packets, prompts, and traces must remain accessible from those records without crowding the main view.
The viewer must define abbreviations at first use and provide a glossary of experiment terms.

The public report must remain readable without a service account.
Its visual resources must accompany the report.
The report must support inspection of the recorded machine-readable evidence.

## Contributor use and public evidence

The contributor documentation must live under `evals/`.
It must describe setup and the required external credentials.
It must distinguish checks without model calls from model evaluations.

The developer interface must support these operations:

- A developer can validate cases and adapter behavior.
- A developer can evaluate a supplied directive.
- A developer can evolve the Packager directive.
- A developer can execute a final comparison.
- A developer can execute separate contract trials without quality scores.
- A developer can prepare a human comparison and record its result.
- A developer can inspect or export an experiment.
- A developer can continue an interrupted experiment.
- A developer can configure and start an experiment in the browser.

Local output and credential files must remain outside tracked source.
Selected public records must identify immutable artifact locations and content hashes.
Public cases must contain redistributable evidence.
Private project experiments must retain separate records.

Each public result must identify the evaluated role and original directive.
It must disclose case counts and trial counts separately.
It must disclose uncertainty and critical failures.
It must identify the exact protocol and source revisions.
It must provide a reproduction procedure and complete candidate history.

Reproduction must mean execution of the recorded protocol.
The documentation must explain that model outputs can vary.
Results for one model configuration must retain that scope.

A README improvement claim must link to measured results and their limitations.
It must name the behavior that improves and the control used for comparison.
It must distinguish evaluated roles from roles outside the reported experiment.
An experiment record alone cannot establish useful autonomous work across the complete Culler system.

## Extension to other roles

The common framework must own datasets, trials, GEPA integration, experiment records, and comparison analysis.
A role adapter must supply these role-specific contracts:

- The adapter identifies the directive component and protected requirements.
- It assembles role inputs and restores the required environment.
- It exposes the role's permitted command behavior.
- It identifies the expected outputs and deterministic contract checks.
- It supplies authoritative reference evidence and the role's assessment rubric.
- It converts assessment results into case scores and permitted optimizer feedback.

The common framework must treat packet-specific fields as role artifacts.
Its core record structure must support other artifact types without a Packager-specific dependency.
An adapter integration check must demonstrate that separation with a minimal test role.
The test role must require no production directive or model calls.

Later role evaluations can use these inputs and outcomes:

| Role | Case inputs | Outcome to assess |
| --- | --- | --- |
| Strategist | A repository, Project vision, existing work, and relevant constraints. | Proposals advance the vision with useful scope and limited duplicate work. |
| Arbiter | A fixed proposal set, Project vision, existing work, and selection constraints. | Decisions select useful work and explain rejection at the appropriate level. |
| Worker | A repository task, acceptance criteria, fixed skills, and executable checks. | The Worker satisfies Culler-specific delivery requirements while preserving task competence. |
| Verifier | A known change, acceptance criteria, known defects, and authoritative evidence. | The Verifier detects material defects and reports evidence limits without false success claims. |

Each added role requires reviewed cases and a validated assessment before automatic evolution supports a result claim.
Worker and Verifier evaluation must target demonstrated Culler-specific needs.
The Packager release must require no complete adapters for those roles.

## Later comparisons

Each adopted directive must retain its qualification record and source revision.
A later proposed edit must compare against that adopted directive as its new original.
The comparison must use the same case inputs and execution conditions for both directives.

Public regression cases must continue to expose known failures.
Fresh final cases must support a new claim of independent improvement.
A changed model or harness must receive a new comparison with the original directive.

A responsibility or command change must include the affected brief and skills in its declared scope.
Its report must describe the result as an evaluation of that complete input.

## Required experiment inputs

Framework implementation can proceed with test fixtures and test doubles.
The first full experiment requires a completed protocol with the inputs below.
The default protocol fixes the first exploratory experiment values below.

| Input | Required record before the full experiment |
| --- | --- |
| Rubric and operator context | Score anchors, dimension weights, assumed knowledge, and examples of critical failures. |
| Packet and edit invariants | Applicable section rules, protected directive boundaries, and semantic scope checks. |
| Judge status | Explicit uncalibrated status. Accepted calibration evidence remains necessary for an improvement claim. |
| Memos cases | Reviewed commit ranges, licenses, source artifacts, reference facts, reports, supplied media, and independent case groups. |
| Dataset sizes | Case counts by type and dataset, including the final holdout set. |
| Trial counts | Repeats for the pilot, training comparisons, validation, optional confirmation, and final comparison. |
| Proposal parameters | The proposal count, minibatch size, sampler, concurrency, random seed, and skip policy. |
| Completion rule | The exact rule, its score source, its thresholds, and the confirmation policy. |
| Final decision | The useful gain, allowed case-type regressions, uncertainty method, required comparison precision, and strict pair majority review. |
| Runtime versions | Exact model identifiers, harness versions, settings, browser tools, permissions, and the pinned GEPA revision. |

The experiment configuration must distinguish unset values from executable values.
Readiness checks must identify the exact missing inputs before a full experiment starts.
Test fixture values must remain separate from the full experiment configuration.
The implementation must use the declared experiment size and statistical thresholds.

### Default exploratory experiment

The corpus must contain 35 independent Memos changes across all seven case types.
The inventory must assign 13 training cases, 13 validation cases, and nine final holdout cases.
Each dataset must cover all seven case types.
All five pilot groups must remain in the training set.
The final set must assess new changes within Memos, not an unseen repository.

| Setting | Fixed value |
| --- | --- |
| Training trials per case and directive | Two |
| Validation trials per case and directive | Two |
| Final trials per case and directive | Three |
| Proposal tasks per iteration | Two |
| Training cases per minibatch | Three |
| Concurrent operations | Four |
| Random seed | 20261002 |
| Minibatch sampler | `epoch_shuffled` |
| Maximum iterations | 12 |
| Iterations without the required gain | Four |
| Required validation gain for the completion counter | Two points out of 100 |
| Confirmation comparison | Disabled |
| Merge proposals | Disabled |
| Native evaluation cache | Disabled |
| Useful final gain | Three points out of 100 |
| Allowed final regression per case type | Three points out of 100 |
| Bootstrap samples | 10000 |
| Independent final groups | Nine |
| Maximum uncertainty interval width | 15 points out of 100 |

The run must freeze product wrapper hashes, framework source hashes, and runtime source hashes before model execution.
The runtime must snapshot its runner and bridge sources and reject changed source bytes during continuation.
The protocol must fix the actual transport channel and model identifier for each role.
The runner must preserve production source, production deployment, and the production spine.

The `run` command must execute evolution and the final comparison through one entry point.
The command must open a local browser viewer and update it from verified durable records.
The viewer must preserve the selected historical event while new records arrive.
The command must export a portable viewer after completion, failure, or interruption.
The `run --resume` command must continue the same frozen experiment.
The browser must provide a Resume experiment action for failed or interrupted experiments.
The server must restore saved experiments after a restart.
The browser must retain the experiment identifier, output directory, chronology, and candidate ancestry during resume.
The browser must show the resume settings before execution.
The server must refuse resume while another process owns the experiment records.
The browser must show saved experiments without a model call.

A runtime update must require an incomplete initial validation and unchanged assessment rules.
The update must preserve the original product source, runner, directive, cases, rubric, models, and effort levels.
The update can enable fast mode and enforce validated Judge JSON.
The update must preserve the original protocols, trial records, scores, and packets.
The Judge must reassess retained packets after the update.
The current search must use those new scores.
The viewer must distinguish the update from earlier scores in chronological order.
The update must preserve the Packager generation identity for retained packets.
The update must retain its exact source manifests.
A separate resume checkout must preserve the original product and runner files.
An interrupted update must support recovery from its durable record.
A search after initial validation must use its original runtime during ordinary resume.
The repository disposal repair must require an incomplete initial validation.
The repair must preserve completed Judge scores and original cleanup records.
The repair must preserve the packet, submission, source attestation, and repository identities.
The repair must preserve the runtime, assessment rules, directive, cases, and trial counts.
The chronology must identify both source protocols before execution.
The `view` command must reopen its live viewer without a model call.
The `serve` command must open browser controls before an experiment exists.
The browser controls must use the same experiment runner as the `run` command.

## Acceptance evidence

The Packager proof of concept must provide evidence for each outcome below.

| Outcome | Required evidence |
| --- | --- |
| The framework remains separate from production. | The delivered framework preserves production sources and deployment contents. |
| The role receives faithful inputs. | Adapter checks compare the trial input and command behavior with product behavior. |
| The GEPA integration preserves the declared protocol. | Token-free integration checks exercise the pinned library through the custom adapter. |
| Packager's writing instructions are editable. | Input checks confirm the merged instructions and the absence of a separate `writing` skill. Candidate edits can change these instructions. |
| The packet uses the actual display conditions. | Desktop checks preserve packet images and observed display failures. |
| Supplied media supports packet production. | Trials produce packets from fixed media without an instance of the reviewed application. |
| The measurement recognizes meaningful defects. | Calibration records expose known factual errors and weak explanations. |
| Candidate evolution is observable. | An experiment preserves accepted and rejected candidates with their actual decision evidence. |
| Scores retain their origin. | Historical views expose only completed evaluations and link each comparison to its actual trials. |
| A scope rejection prevents child execution. | A rejected edit retains its text and reason with zero child trials and absent child scores. |
| Final cases remain independent of the search. | Access checks demonstrate the separation between optimizer feedback and final cases. |
| A comparison supports its stated result. | Fresh repeated trials support the recorded qualification decision and uncertainty. |
| Interrupted work remains auditable. | A recovery check preserves operation identities and exposes absent records. |
| Record failures prevent false completion. | An injected record failure stops dependent work despite native callback exception suppression. |
| A contributor can reproduce the procedure. | A clean checkout supports the documented experiment with external credentials supplied separately. |
| Public claims remain bounded by evidence. | The result report identifies the evaluated scope and links to the complete records. |

A successful framework does not require a candidate to beat the original directive.
It requires a credible result and complete evidence for that result.

The token-free integration checks must cover these GEPA outcomes:

- Several proposal tasks select the same parent without a record collision.
- A child improves on its minibatch and receives full validation.
- A child loses or ties on its minibatch and receives no validation.
- An accepted child has a lower validation mean than its parent.
- A candidate remains stored with zero parent selection probability.
- A duplicate proposal or native skip retains an accurate outcome record.
- A run that returns the original directive records no evolved candidate and skips the final comparison.
- Continuation preserves completed trials and the candidate pool.

The checks must use the pinned GEPA library with deterministic test doubles for model behavior.


## Persistent experiment storage

The default experiment directory must be `~/.local/share/culler-evals/experiments/`.
The default repository cache must be `~/.local/share/culler-evals/cache/`.
The framework must reject experiment record paths inside temporary directories before model calls.
The check must resolve symbolic links and the operating system temporary directory.
Product deployment resets and uninstall operations must leave these directories intact.

Each experiment must preserve its journal, manifests, candidate directives, packets, assessments, and native checkpoint artifacts.
Each required record write must preserve a separate local copy before dependent work continues.
The local backup directory must reside outside the primary experiment directory.
The default local backup directory must be `~/.local/share/culler-evals/backups/`.
A required backup failure must stop dependent model work.
An absent or replaced record directory must stop dependent model work.
The framework must not silently create a new journal after directory loss.
Both local copies use the same disk. Disk failure protection requires an external backup.

Each experiment must preserve its exact source files and source revision before model calls.
The source archive must include the framework, product helpers, runner, case inputs, and browser assets.
The archive must exclude untracked credentials and repository caches.
Resume must restore an absent task worktree from the preserved source when necessary.
Source recovery must preserve existing checkouts and verify the frozen source manifests.
Case inputs outside the source checkout must use persistent paths.

The browser must display the primary record directory and local backup directory.
A server restart must retain experiment history without model calls.
Record recovery must verify the journal and artifact hashes before it restores an absent primary directory.
Record recovery must preserve existing primary directories without replacement.
Record recovery must invoke no models.
The browser must recognize recovered experiment records and permit explicit resume.
Completed trials must remain reusable after record recovery.
The framework must reconstruct required Judge evidence from the fixed case inputs when scratch repositories are absent.
