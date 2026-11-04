# Releasing CDS — Release Process and Versioning

`version.txt` holds the **last released** version. Nothing moves it on a PR merge — the release builder owns it, and a release is the only thing that changes it. The version number therefore describes what was released, not how many PRs were merged.

---

## The model

A release run resolves the version to release from its dispatch inputs, commits it to the release branch, builds from it, and tags that commit.

```
merge PR  → nothing happens to version.txt
merge PR  → nothing happens to version.txt

dispatch release
  → resolve version (bump_type / version / use_existing_version)
  → refuse if that tag already exists
  → write version.txt
  → commit + push it to the release branch
  → make build
  → tag the bump commit, push the tag
  → create the GitHub release
```

The tag is the release identity. A version that already has a tag is refused, so the same version can never be released twice.

Between releases, every build off `main` reports the same version. That is expected — local dev zips share a filename until the next release.

---

## Inputs

Actions → **🚀 CDS Release Builder** → Run workflow.

| Input | Default | Use it when |
|---|---|---|
| `bump_type` | `minor` | Normal scheduled release. Bumps `version.txt`. `patch` for a maintenance release. |
| `version` | — | You need an exact version — a GA off a prerelease, or a prerelease itself. Overrides `bump_type` and `use_existing_version`. A bare `0.4.0` is accepted. |
| `use_existing_version` | `false` | Retrying a release whose version bump already landed. See [Retrying a failed release](#retrying-a-failed-release). |
| `branch` | `main` | Releasing a maintenance version from a release branch. |
| `commit_version_bump` | `true` | Set `false` to release without moving the branch. |
| `prerelease` | `false` | Marks the GitHub release as a pre-release. |

`version`, `bump_type` and `use_existing_version` are mutually exclusive, in that order of precedence.

---

## Running a release

### Scheduled release

The common case takes no inputs at all:

```bash
gh workflow run release.yml --repo wso2/identity-customer-data-service
```

`v0.3.65` → releases and tags `v0.4.0`, and commits `version.txt` as `v0.4.0`.

### Maintenance release

A third-digit release, run against the relevant release branch:

```bash
gh workflow run release.yml --repo wso2/identity-customer-data-service \
  -f branch=0.3.x -f bump_type=patch
```

`branch` controls what is checked out, built, bumped and pushed, so this reads `0.3.x`'s own `version.txt` and pushes the bump there. `main` is untouched.

Leave `--ref` alone so the canonical workflow definition on `main` is the one that runs, even when releasing another branch.

### Major release

```bash
gh workflow run release.yml --repo wso2/identity-customer-data-service -f bump_type=major
```

### Prerelease, or an exact version

```bash
gh workflow run release.yml --repo wso2/identity-customer-data-service \
  -f version=v0.4.0-beta1 -f prerelease=true
```

Use an explicit `version` to go from a prerelease to GA. A `patch` bump off `v0.4.0-beta1` computes `v0.4.1`, not `v0.4.0`.

### Retrying a failed release

If a run commits the bump to `v0.4.0` and *then* the build fails, the branch says `v0.4.0` but no tag exists. Re-running with the default `minor` would release `v0.5.0` and silently skip `v0.4.0`. Instead, release `version.txt` exactly as it stands:

```bash
gh workflow run release.yml --repo wso2/identity-customer-data-service \
  -f use_existing_version=true
```

This commits nothing and releases the version already on the branch.

---

## What happens after the release

`update-deployment-versions.yml` runs automatically when the release builder completes. It resolves the released tag as the newest product tag reachable from the released branch, and updates `CDS_APPLICATION_TAG` in the deployment repository's CI variables.

Because the tag lands on the version bump commit created *during* the release, the tag is resolved from the branch rather than from the commit the run was dispatched against.

---

## Verifying a release

| Check | Where |
|---|---|
| Version bump commit | `Version bump: vX.Y.Z` on the release branch |
| Tag | Points at that bump commit |
| GitHub release | Tagged `vX.Y.Z`, carries `cds-vX.Y.Z.zip` |
| Deployment repo | `CDS_APPLICATION_TAG` updated in `ci-pipelines/cds/setup-variables.yaml` |

---

## Helm chart versioning

The Helm chart is versioned independently of the product. `helm-chart-releaser.yml` fires on pushes to `main` that touch `install/helm/confs/**`, `install/helm/templates/**` or `install/helm/values.yaml`, and bumps the chart version in `install/helm/Chart.yaml`. A product release does not move the chart version, and vice versa.

---

## Troubleshooting

| Failure | Cause and fix |
|---|---|
| `Version 'vX.Y.Z' already exists as a git tag` | That version was already released. Pass a new `version`, or a different `bump_type`. |
| `Could not parse version '<x>' from version.txt` | `version.txt` is malformed. It must be `vX.Y.Z`, optionally with a `-PRERELEASE` suffix. |
| `Version 'vX.Y' does not follow vX.Y.Z[-PRERELEASE]` | An explicit `version` input was short a digit. |
| `version.txt not found or empty` | The release branch is missing `version.txt`. |
| `No product tag found on branch` (deployment update) | The release builder did not push its tag. Check the release run before re-dispatching the deployment update manually with its `tag` input. |
