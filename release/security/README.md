# Release security checks

The release workflow uses Gitleaks and Trivy before publication.
The tools use the versions and archive checksums in `tools.json`.
The installer makes at most 3 attempts for each download after a temporary network or server error.
A checksum mismatch fails the installer at once.
Every third-party workflow action uses a full commit identifier.
Pull request jobs hold read-only permissions and receive no provider credentials or publication credentials.

## Source publication

The local secret check covers every commit reachable from the specified refs.
Each intended public branch and tag must appear in the command.
The check rejects shallow repositories and modified commit graphs.
The check includes deleted files, merge changes, binary blobs, and commit messages.

The coding harness can prepare the fixed scanners in a scratch directory:

```sh
python3 release/security_tools.py /private/tmp/culler-security-tools
```

This command checks `main` and one example release tag:

```sh
./release/secret-check.sh \
  --ref refs/heads/main \
  --ref refs/tags/REPLACE_WITH_THE_INTENDED_TAG \
  --tools /private/tmp/culler-security-tools \
  --report /private/tmp/culler-history-security.json
```

The actual command names each intended tag.
The command omits the tag argument when publication includes no tags.
Each changed ref requires another check before publication.
An unresolved secret result prevents publication.
A successful check establishes that the configured rules found no unresolved matches.
The check never reads credential values from a live Culler deployment.

GitHub Actions uses `--public-refs` with a full checkout.
That selection includes all fetched origin branches, all tags, and the checked-out commit.
For a pull request, the checked-out merge commit retains every new source commit.

## Required checks and exact inputs

The `release-security` job requires each prerequisite to finish with `success`.
Failures, cancellations, and skipped prerequisites fail that job.
The prerequisites cover these inputs:

- The complete fetched source history.
- The application archive and both installer files.
- The `/project` image and the `/runner` image.
- The controlled scanner acceptance tests.
- The existing package, image, deployment, and browser checks.

The package job scans archive contents before it preserves the release inputs.
The image job builds both images with read-only permissions.
The image job restores the build cache of the latest `main` build from the GitHub Actions cache.
Only a push to `main` saves a cache entry.
A pull request run restores that cache and saves nothing.
The product image builds on the default Project image of the same run.
The job checks the Project browser and the runtime browser, then saves each image once and scans that archive.
The upload job verifies each archive checksum and loaded image identity before the registry upload.
The upload job performs no image build.
The release job checks file checksums against the earlier scan record.
It scans all release files, including the generated release record, before its first release file upload.
The existing download checks remain in effect.

The repository ruleset in [release-ruleset.json](../../.github/release-ruleset.json) requires `release-security` from GitHub Actions.
The ruleset requires the current base commit and provides no bypass actors.
The repository owner imports this ruleset through **Settings → Rules → Rulesets**.
The active repository ruleset must enforce this check before a pull request can merge.
A local ruleset file alone provides no GitHub enforcement.
Private repositories require a GitHub plan that supports rulesets.
The checks cannot establish enforcement when GitHub refuses access to the repository rules.

The repository setting **Cache retention** must be 90 days.
The repository owner sets it through **Settings → Actions → General → Cache settings**.
The default retention is 7 days.
With the default, a release after a pause of more than 7 days builds both images without a cache.
GitHub offers this setting to repositories with a recorded payment method or a paid plan.

## Secret coverage and reports

Gitleaks uses its established detection rules.
The installer removes broad path exclusions from its fixed default configuration.
The checks ignore inline allow comments and repository ignore files.
The checks preserve rule-specific false detection rules from Gitleaks.
An ordinary variable name alone is not a credential.

Trivy checks image files and image configuration for secrets.
The command explicitly selects `--scanners vuln,secret` and `--image-config-scanners=secret`.
Gitleaks also checks every retained layer independently.
This additional check covers base layers and files that a later layer deletes.
Gitleaks checks the complete image configuration and manifest.
That configuration includes stored environment values and build history.

The file check expands tar and ZIP archives and gzip, bzip2, and xz streams.
The file and image checks scan the target name of each symbolic link and hard link as text, and expand no link.
Gitleaks receives the expanded contents with its internal archive traversal disabled.
It scans file contents under neutral names, so a file path cannot exclude a dependency directory.
Binary text retains its line positions after the check replaces invalid text bytes and zero bytes.
The report maps each result back to its original file, archive member, commit, or image layer.
Unreadable archives and exceeded resource limits produce an unverified result.
The archive depth limit is 16.
The expanded content limit is 80 gibibytes per scan group.
The history check limits each Git object to 512 mebibytes.

The reports retain locations and rule identifiers.
They omit secret values, source excerpts, image environment values, and raw scanner output.
Each report records the scanner version and executable checksum.
File and image reports record the exact input checksums.
History reports record the selected refs and commit identifiers.
Gitleaks reports also record the rule configuration checksum.

## Vulnerability coverage

Trivy checks installed operating system packages and supported bundled dependencies against current advisory data.
The reports retain every detected severity.
Each vulnerability result includes these fields when available:

- The image and target path.
- The package and installed version.
- The advisory identifier and severity.
- The fixed version.

High and critical results fail the check unless an exact, unexpired exception applies.
The report retains suppressed results and their exceptions.
The report lists every package that Trivy identifies.
The image manifests supply explicit versions for bundled tools and browser executables.
The report lists those version records separately for assessment.
A version record alone does not establish advisory coverage.
An absent package inventory or required tool version record produces an unverified result.

Each image scan uses a fresh advisory cache.
The report records the advisory metadata, database checksum, and retrieval time.
Unavailable advisory data, stale data, tool failure, and incomplete required coverage produce an unverified result.
An unverified required check prevents release publication.
A successful vulnerability check covers known advisories for the dependencies that Trivy identifies.
Custom Project images remain outside the release workflow.

## Exceptions

`exceptions.json` contains the explicit exceptions.
A secret exception matches these exact fields:

- The scanner and subject identifier.
- The original file path.
- The rule identifier and line number.

Each secret exception includes a reason.
The subject identifies a commit, artifact checksum, or image layer.
The initial exceptions identify checked source hashes and synthetic test values.

A vulnerability exception matches the image, target, package, installed version, and advisory identifier.
Each vulnerability exception includes a reason and an expiry date in `YYYY-MM-DD` form.
The exception remains valid through that date in Coordinated Universal Time (UTC).
An expired or unrelated exception preserves the result and the failed check.
Exceptions contain no credential values.

## Nightly checks and acceptance evidence

`security-nightly.yml` checks the current release each night.
It reads both immutable image references from the current release record.
It retrieves those images and uses fresh advisory data.
GitHub Actions retains the reports for 90 days, including failed and unverified results.
The workflow also supports a manual run.

`./release/check.sh` checks the policy and publication controls without network access or scanner downloads.
The scanner acceptance tests use the real, fixed Gitleaks and Trivy executables.
The tests create synthetic credentials in deleted commits, archive members, deleted image layers, and image metadata.
The tests check that reports retain locations and omit credential values.
The advisory fixture creates two controlled databases for one unchanged image.
The second database adds a high-severity advisory, and the same image then fails.
The test changes only the database download response.
Trivy still identifies the package and advisory itself.

The coding harness can run the scanner acceptance tests with these commands:

```sh
(cd release/security/fixture-db && mise exec -- go build -mod=readonly -o /private/tmp/culler-fixture-db .)
python3 release/security_acceptance.py \
  --tools /private/tmp/culler-security-tools \
  --fixture-db /private/tmp/culler-fixture-db
```

The official documentation describes the scanner behavior:

- [Gitleaks commands and detection rules](https://github.com/gitleaks/gitleaks/tree/v8.30.1).
- [Trivy image targets and configuration checks](https://trivy.dev/docs/latest/guide/target/container_image/).
- [Trivy secret checks and base layer exclusions](https://trivy.dev/docs/latest/guide/scanner/secret/).
- [GitHub ruleset requirements](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets).
