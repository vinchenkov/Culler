# Release security scans

The release workflows use Gitleaks and Trivy to report security results.
A scan result never prevents a push or a release.
The repository owner reads the reports and decides each action.
The tools use the versions and archive checksums in `tools.json`.
The installer makes at most 3 attempts for each download after a temporary network or server error.
A checksum mismatch fails the installer at once.
Every third-party workflow action uses a full commit identifier.
Pull request jobs hold read-only permissions and receive no provider credentials or publication credentials.

## Scans and reports

| Job | Scan | Report |
| --- | --- | --- |
| `source-secrets` | Gitleaks checks the complete fetched history of this repository for secrets. | The job summary and the `culler-security-history-<attempt>` artifact. |
| `image-vulnerabilities` | Trivy checks the default Project image and the product image for known vulnerabilities. | The job summary and the `culler-image-vulnerabilities-<attempt>` artifact. |
| `current-release-images` in `security-nightly.yml` | Trivy checks the images of the current release with fresh advisory data. | The job summary and the `culler-security-nightly-<attempt>` artifact. |
| `security-acceptance` | The scanner acceptance tests check the scans with controlled fixtures. | The `culler-security-acceptance-<attempt>` artifact. |

No release job waits for these jobs.
A secret result or a vulnerability result makes no job fail.
A scan that cannot complete writes an unverified report, and its own job fails.
The secret scan covers only the source history of this repository.
The dependencies, the release files, and the images are outside the secret scan.

## Required release checks

The `release-checks` job requires each of these jobs to finish with `success`:

- `fast-suite-macos`, which also runs `./release/check.sh` and builds the application package.
- `deployment-linux`.
- `dashboard-browser`.
- `release-images`.

Failures, cancellations, and skipped prerequisites fail the `release-checks` job.
The publication jobs require the `release-checks` job.

The image job builds both images with read-only permissions.
The image job restores the build cache of the latest `main` build from the GitHub Actions cache.
Only a push to `main` saves a cache entry.
A pull request run restores that cache and saves nothing.
The product image builds on the default Project image of the same run.
The job checks the Project browser and the runtime browser, then saves each image once.
The upload job checks the source revision and the loaded image identity of each archive before the registry upload.
The upload job performs no image build.
The existing download checks remain in effect.

The repository ruleset in [release-ruleset.json](../../.github/release-ruleset.json) requires `release-checks` from GitHub Actions.
The ruleset requires the current base commit and provides no bypass actors.
The repository owner imports this ruleset through **Settings → Rules → Rulesets**.
A local ruleset file alone provides no GitHub enforcement.
Private repositories require a GitHub plan that supports rulesets.

The repository setting **Cache retention** must be 90 days.
The repository owner sets it through **Settings → Actions → General → Cache settings**.
The default retention is 7 days.
With the default, a release after a pause of more than 7 days builds both images without a cache.
GitHub offers this setting to repositories with a recorded payment method or a paid plan.

## Source history scan

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
A complete scan lists each match of the configured rules.
The check never reads credential values from a live Culler deployment.

GitHub Actions uses `--public-refs` with a full checkout.
That selection includes all fetched origin branches, all tags, and the checked-out commit.
For a pull request, the checked-out merge commit retains every new source commit.

## Secret coverage and reports

Gitleaks uses its established detection rules.
The installer removes broad path exclusions from its fixed default configuration.
The scan ignores inline allow comments and repository ignore files.
The scan preserves rule-specific false detection rules from Gitleaks.
An ordinary variable name alone is not a credential.

The scan reads each binary blob from the Git object graph.
It expands tar and ZIP archives and gzip, bzip2, and xz streams in those blobs.
It scans the target name of each symbolic link and hard link in an archive as text, and expands no link.
Gitleaks receives the expanded contents with its internal archive traversal disabled.
Binary text retains its line positions after the scan replaces invalid text bytes and zero bytes.
The report maps each result back to its commit, file, and archive member.
Unreadable archives and exceeded resource limits produce an unverified result.
The archive depth limit is 16.
The expanded content limit is 80 gibibytes per scan group.
The scan limits each Git object to 512 mebibytes.

The reports retain locations and rule identifiers.
They omit secret values, source excerpts, and raw scanner output.
Each report records the scanner version, the executable checksum, and the rule configuration checksum.
Each report also records the selected refs and commit identifiers.

## Exceptions

`exceptions.json` records the known secret results of this repository.
A secret exception matches these exact fields:

- The scanner and subject identifier.
- The original file path.
- The rule identifier and line number.

The subject identifies the commit that added the line.
Each secret exception includes a reason.
The report and the job summary show the reason of each matched result.
An exception changes only the report text.
Exceptions contain no credential values.

## Vulnerability coverage

Trivy checks installed operating system packages and supported bundled dependencies against current advisory data.
Each vulnerability result includes these fields when available:

- The image and target path.
- The package and installed version.
- The advisory identifier and severity.
- The fixed version.

The job summary shows the number of results for each severity.
It also shows the number of results with a fixed version for each severity.
The report lists every result and every package that Trivy identifies.
The report and the job summary list each package without an identified version.
Trivy checks no advisories for a package without a version.
The report records the operating system data of Trivy, including its end of support state.
The image manifests supply explicit versions for bundled tools and browser executables.
The report lists those version records separately for assessment.
A version record alone does not establish advisory coverage.

Each image scan uses a fresh advisory cache.
The report records the advisory metadata, database checksum, and retrieval time.
Unavailable advisory data, stale data, tool failure, and an absent package inventory produce an unverified result.
Custom Project images remain outside the release workflow.

## Nightly scans and acceptance evidence

`security-nightly.yml` checks the current release each night.
It reads both immutable image references from the current release record.
It retrieves those images and uses fresh advisory data.
Without a current release, the job writes a note in its summary and completes.
GitHub Actions retains the reports for 90 days, including unverified results.
The workflow also supports a manual run.

`./release/check.sh` checks the release tools without network access or scanner downloads.
The scanner acceptance tests use the real, fixed Gitleaks and Trivy executables.
The tests create synthetic credentials in deleted commits, binary blobs, and nested archive members.
The tests check that reports retain locations and omit credential values.
The advisory fixture creates two controlled databases for one unchanged image.
The second database adds a high-severity advisory, and the report of the same image then lists that advisory.
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
- [Trivy image targets](https://trivy.dev/docs/latest/guide/target/container_image/).
- [GitHub ruleset requirements](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets).
