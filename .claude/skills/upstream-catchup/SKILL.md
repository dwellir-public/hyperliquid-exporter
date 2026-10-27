---
name: upstream-catchup
description: Catch this fork up with upstream validaoxyz/hyperliquid-exporter. Diffs the last reviewed upstream release against the latest one, classifies every change as should port, could port or skip against our tree, has an independent agent review the list once, then presents it for a decision. Use when the user mentions new upstream releases, an "Upgrade: Hyperliquid metrics exporter vX.Y.Z" ticket, or asks what to port from upstream, even without naming the skill.
---

# Upstream catch-up

This fork shares no history with upstream, so every upstream change is ported by hand or skipped. `docs/operations/upstream-sync.md` is the source of truth for the method: the path mapping, the classification classes, the standing decisions and the review log. Read it in full before step 2. This skill wraps that runbook into a fixed sequence that ends on a decision from the user. It writes no code until the user picks items.

## 1. Find the range

- **From:** the last row of the review log in `docs/operations/upstream-sync.md`. Its SHA column is the last reviewed upstream commit.
- **To:** the latest upstream release tag.

```sh
git fetch --no-tags upstream   # our v2.x tags collide with upstream's; never fetch tags
TAG=$(git ls-remote --tags upstream | awk -F/ '!/\^\{\}$/ {print $3}' | sort -V | tail -1)
TO=$(git ls-remote upstream "refs/tags/$TAG^{}" | cut -c1-7)
git log --oneline "$FROM..$TO"
git log --oneline "$TO..upstream/main"   # unreleased; mention, do not classify
```

If `upstream` is missing, add it: `git remote add upstream git@github.com:validaoxyz/hyperliquid-exporter.git`. If `FROM..TO` is empty, report "nothing new since <release>" and stop.

Done when: you have `FROM`, `TO`, the release names between them and the commit list.

## 2. Inventory upstream changes

Read the upstream `CHANGELOG.md` sections for every release in the range (`git show $TO:CHANGELOG.md`), then the code diff: `git diff --stat $FROM..$TO` and the full diff of every non-test file. Treat tests and fixtures as evidence of intent, not as items. Also check `go.mod` (Go and dependency bumps, often for a CVE) and `.github/`.

Done when: every changelog bullet and every non-test file in the diff maps to an item. List an item once even when it spans releases.

## 3. Check each item against our tree

For each item, find our counterpart (upstream paths differ; the runbook lists them) and decide whether it touches code we run. Typical outcomes:

- We have the same code path and the same defect or gap: candidate to port.
- We lack the feature (response parsing, mempool, consensus RPC and other monitors we do not run): skip, naming the absent component.
- Already covered, already newer, or excluded by a standing decision: skip, citing which.

Then place it in one bucket:

- **Should port**: schema-compat (a metric is wrong or reads `other` on current hl-node), correctness in a metric we have, or a security fix that reaches our binary.
- **Could port**: perf or tidiness on a path we run where the gain is small, or anything the user would reasonably weigh either way.
- **Skip**: everything else, with a one-clause reason.

Done when: every item from step 2 has a bucket, the `file:line` of our counterpart (or the reason none exists) and, for port candidates, the files it would touch, the tests it needs (old and new shape for schema changes, per `CLAUDE.md`), and whether `CHANGELOG.md` and `docs/metrics-overview.md` change.

## 4. Independent review, once

Dispatch one fresh subagent (opus) with read access to the repo. Give it the range, the item list with buckets and evidence, and the path to the runbook. It does not get your reasoning beyond what the list states. Ask it to try to refute each placement:

- a skip that touches code we do run,
- a port whose defect does not exist in our tree,
- an upstream change missing from the list,
- a standing decision or convention ignored.

It reports disagreements only, each with evidence. Fold in the ones its evidence supports; for any you reject, keep a one-line reason. Do not run a second round.

## 5. Present

Follow the decision-reply format in the user's global instructions:

1. One or two sentences naming the recommendation, including "nothing to port" when that is the answer.
2. The range reviewed (`FROM..TO`, releases) and its size.
3. A table: release, change, our tree, verdict.
4. Numbered decisions for every should and could item, plus a final item to log the review in `upstream-sync.md`. Each says what it changes, what it buys and the paths it touches.
5. What the reviewer changed, and any objection you rejected with the reason. Omit when it agreed.
6. `Default: <should items>, <log item>`.

Stop there and wait for the user to choose.

## After the user decides

Do the chosen items in a worktree per the user's global rules, with one commit per logical unit. The review-log row is a commit too, even when nothing is ported. Its SHA is `TO`, and its outcome names what was ported and why the rest was skipped, in the style of the existing rows. Each port also gets a `CHANGELOG.md` Unreleased entry. Open the PR only when the user asks; its title follows the most user-visible item.
