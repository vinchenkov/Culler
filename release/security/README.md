# Release security scans

The release workflows use Trivy to report known vulnerabilities in the release images.
GitHub secret scanning reports secrets in this repository.
A scan result never prevents a push or a release.
The repository owner reads the reports and decides each action.
Trivy uses the version and archive checksums in `tools.json`.
The installer makes at most 3 attempts for each download after a temporary network or server error.
A checksum mismatch fails the installer at once.
Every third-party workflow action uses a full commit identifier.
Pull request jobs hold read-only permissions and receive no provider credentials or publication credentials.

## Scans and reports

| Scan | What it checks | Report |
| --- | --- | --- |
| `image-vulnerabilities` job | Trivy checks the default Project image and the product image for known vulnerabilities. | The job summary and the `culler-image-vulnerabilities-<attempt>` artifact. |
| `current-release-images` job in `security-nightly.yml` | Trivy checks the images of the current release with fresh advisory data. | The job summary and the `culler-security-nightly-<attempt>` artifact. |
| `security-acceptance` job | The scanner acceptance tests check the vulnerability scan with controlled fixtures. | The `culler-security-acceptance-<attempt>` artifact. |
| GitHub secret scanning | GitHub checks the Git history of this repository for the token formats of supported providers. | Alerts on the **Security** tab of the repository. |

No release job waits for these scans.
A vulnerability result makes no job fail.
A scan that cannot complete writes an unverified report, and its own job fails.

## Secret scanning

GitHub secret scanning is free for public repositories.
The repository owner turns it on through **Settings → Advanced Security**.
It finds the token formats of supported providers, such as GitHub tokens and AWS keys.
It does not look for generic secrets without a known format.
Push protection stays off, so a secret result never prevents a push.

## Required release checks

The `release-checks` job requires each of these jobs to finish with `success`:

- `fast-suite-macos`, which also runs `./release/check.sh`.
- `application-package`, which builds the application package alongside the checks.
- `deployment-linux`.
- `dashboard-browser`.
- `release-images`.

Failures, cancellations, and skipped prerequisites fail the `release-checks` job.
The publication jobs require the `release-checks` job.

The image job starts after the package job.
The image job builds both images with read-only permissions.
The image job restores the build cache of the latest `main` build from the GitHub Actions cache.
Only a push to `main` saves a cache entry.
A pull request run restores that cache and saves nothing.
The product image builds on the default Project image of the same run.
The job checks the Project browser and the runtime browser, then saves each image once.
The upload job checks the source revision and the loaded image identity of each archive before the registry upload.
The upload job performs no image build.
The existing download checks remain in effect.

The Go build caches and dependency caches use separate entries.
Each successful push to `main` saves Go build results under a commit-specific key.
Each job restores the latest compatible cache for its platform and toolchain.
The cleanup job retains two Go build caches per job and platform on `main`.
The cleanup preserves image caches, dependency caches, and caches from other branches.
Cache cleanup holds Actions write permission only after a push to `main`.
Publication proceeds independently of cache cleanup.

The repository ruleset in [release-ruleset.json](../../.github/release-ruleset.json) applies to the `main` branch.
A commit reaches `main` only after `release-checks` from GitHub Actions passes for that commit.
A pull request branch must contain the current `main` commit before the merge.
The repository admin role can bypass the ruleset, so the repository owner can push directly to `main`.
GitHub applies only the copy in **Settings → Rules → Rulesets**, and that copy must match this file.

The repository setting **Cache retention** must be 90 days.
The repository owner sets it through **Settings → Actions → General → Cache settings**.
The default retention is 7 days.
With the default, a release after a pause of more than 7 days builds both images without a cache.
GitHub offers this setting to repositories with a recorded payment method or a paid plan.

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
The report also records the scanner version and the executable checksum.
Unavailable advisory data, stale data, tool failure, and an absent package inventory produce an unverified result.
The reports omit raw scanner output and image environment values.
Custom Project images remain outside the release workflow.

## Nightly scans and acceptance evidence

`security-nightly.yml` checks the current release each night.
It reads both immutable image references from the current release record.
It retrieves those images and uses fresh advisory data.
Without a current release, the job writes a note in its summary and completes.
GitHub Actions retains the reports for 90 days, including unverified results.
The workflow also supports a manual run.

`./release/check.sh` checks the release tools without network access or scanner downloads.
The scanner acceptance tests use the real, fixed Trivy executable.
The advisory fixture creates two controlled databases for one unchanged image.
The second database adds a high-severity advisory, and the report of the same image then lists that advisory.
The test changes only the database download response.
Trivy still identifies the package and advisory itself.
Other tests check that a failed tool or an incomplete image archive produces an unverified report.

The coding harness can run the scanner acceptance tests with these commands:

```sh
python3 release/security_tools.py /private/tmp/culler-security-tools
(cd release/security/fixture-db && mise exec -- go build -mod=readonly -o /private/tmp/culler-fixture-db .)
python3 release/security_acceptance.py \
  --tools /private/tmp/culler-security-tools \
  --fixture-db /private/tmp/culler-fixture-db
```

The official documentation describes the scanner behavior:

- [Trivy image targets](https://trivy.dev/docs/latest/guide/target/container_image/).
- [GitHub secret scanning](https://docs.github.com/en/code-security/secret-scanning/introduction/about-secret-scanning).
- [GitHub ruleset requirements](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets).
