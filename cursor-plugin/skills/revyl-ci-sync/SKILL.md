---
name: revyl-ci-sync
description: Keep a repo's Revyl remote build mirrored with its CI pipeline - update both in the same PR and debug drift between them.
---

# Revyl CI Sync Skill

Use this skill when changing CI/build configuration in a repo that also has a Revyl remote build configured (`.revyl/config.yaml` with `build.platforms` or a remote dev loop in use).

## The Principle

Your Revyl remote build should mirror your CI pipeline. Both build the same app from the same source; when they diverge, "works on Revyl, fails in CI" (or the reverse) wastes a debugging session that a one-line sync would have prevented.

## The Rule

When a PR changes how CI builds the app — toolchain versions, build flags, env vars, dependency install steps, code signing, prebuild/codegen steps — mirror that change in the Revyl build configuration **in the same PR**. Not a follow-up.

## The Sync-Table Pattern

Keep a small table in the repo (near the CI config or in the repo's agent docs) mapping each CI build step to its Revyl counterpart, for example:

| CI step | Revyl counterpart |
|---|---|
| `node 20.x` setup | `build.platforms.ios.command` env / toolchain |
| `pod install --repo-update` | same step in the Revyl build command |
| `EXPO_PUBLIC_*` env vars | `.revyl/config.yaml` build env / launch vars |

The table's contents are repo-specific and live in the customer repo — this skill only prescribes the pattern: every row that changes on one side changes on both sides in the same PR.

## Debugging Drift

When a build behaves differently on Revyl vs CI:

1. Diff the two build commands and their environments first (the sync table makes this a 30-second check).
2. Compare toolchain versions actually used (logs on both sides), not the ones assumed.
3. Fix by re-syncing the configs, then update the sync table so the drift class cannot silently recur.
