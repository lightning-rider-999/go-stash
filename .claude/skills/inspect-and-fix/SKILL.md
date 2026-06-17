---
name: inspect-and-fix
description: >-
  Runs GoLand's headless inspections (the bundled `scripts/goland-inspect.py`) on a Go project and fixes the underlying CAUSE of every finding — the source of the IDE's editor squiggles, which an agent can't otherwise see. Use whenever the user mentions GoLand/IntelliJ/IDE inspections, "squiggles", weak warnings, "inspection warnings", "clean up the warnings", or after making Go changes. Verifies every finding firsthand (including `go doc` / a compile-probe when a fix depends on an API existing or a language-version behavior); never mutes, suppresses, `//nolint`s, or narrows an inspection to silence a squiggle — fixes the cause, then re-runs with a fresh index to prove it.
---

# inspect-and-fix

## What this is

GoLand's inspection engine paints the editor squiggles and fills the Problems panel — which an agent editing code can't see, so issues it introduces surface later as squiggles the human has to point out. The bundled runner (`scripts/goland-inspect.py`) runs that engine headlessly and prints a triaged report; output is ephemeral (a throwaway temp dir, a fresh index every run, nothing persisted). The goal isn't fewer reported problems — it's **every fixable cause fixed, proven by a fresh re-run, with nothing muted.** A squiggle silenced by suppression is a failure, not a fix.

## Non-negotiables

- **Verify every finding firsthand.** Open the cited `file:line`; the inspection's name is a hint, not a verdict — classify from the actual code.
- **Verify facts before acting on them.** Before applying a suggested rewrite, confirm it's real *in this toolchain*: the API exists (`go doc <sym>`), the construct compiles (a throwaway probe), the language version supports it. A plausible assumption — yours or a subagent's — is not evidence. (Old-Go assumptions bite: `new(expr)` taking a value is valid in modern Go, not older releases.)
- **Never mute, never self-accept.** No disabling/suppressing/`//nolint`/profile-narrowing — and don't quietly decide a finding is "fine to leave"; that's the user's call. If a finding's only fix carries a real cost (degrades docs, removes coverage, reverts a deliberate choice), or it's a GoLand defect with no source fix, SURFACE it with the specific tradeoff and let them decide. Silence and self-acceptance are both failures.
- **Never run destructive git.** No `git checkout` / `reset` / `restore` / `stash` — they discard uncommitted work irrecoverably (it has happened: a subagent ran `git checkout` on an edited file and reported "no work lost"; only stale content gave it away). Forbid these explicitly when you dispatch subagents.
- **Re-run is the only proof.** Don't trust a "done / green" claim — yours or a subagent's; they're confidently wrong sometimes. The fixed category dropping to 0 on a FRESH-index re-run, with no new findings, is the proof. Nothing else counts.

## The loop

1. **Run it** — from inside the repo: `python3 .claude/skills/inspect-and-fix/scripts/goland-inspect.py`. Fresh index every run (see Gotchas), report to stdout, a few minutes to index. (Pass a results directory as an argument to just re-print an existing run's report.) Note the baseline per-inspection counts.

2. **Triage SIGNAL vs INHERENT NOISE.** The report groups problems by inspection (worst severity first) with `file:line`, the flagged token, the description, and a `decide?`/`fix?` hint, then lists the likely clean-fix candidates. The hint is a focusing aid — **read each candidate's cited line firsthand**; a `decide?` on something with a clean fix is still your miss. Most findings have a clean source fix; what's left is a short list of genuine tradeoffs for step 6, never self-accepted.

3. **Fix each real SIGNAL — the cause, verified.** Read the code and confirm the finding is real; if the fix relies on a fact, verify it first (above); then apply the smallest change that removes the cause. Watch the cascade — e.g. removing a shared test helper means replacing **all** its callers, including build-tagged files, or you trade one squiggle for an unused-function squiggle / a broken tagged build.

4. **Verify the fixes compile and pass — including build-tagged files.**
   ```
   go build ./... && go vet ./... && gofmt -l . && golangci-lint run && go test ./...
   go test -tags <tag> -run '^$' ./...   # tagged files still compile (runs NO tests)
   ```

5. **Re-run and confirm.** The categories you fixed must drop to 0 with no new findings. If one *didn't* drop after a real edit, don't shrug — the runner forces a fresh index, so a non-drop is a genuine signal (a fix that didn't take). Investigate.

6. **Loop** until every finding is either fixed (proven by a fresh re-run) or surfaced to the user as a judgment-call — with the before→after counts and, for each remaining finding, WHY its fix carries a cost or is a GoLand defect with no source fix (a stale CI-action schema; modern-Go syntax unresolved inside an isolated doc fence). "Done, the rest is just noise" is the failure this skill exists to prevent.

## Gotchas

- **Stale index serves stale results.** A reused IDE index re-indexes some changed files and not others, so a re-run can report problems you already fixed. The runner avoids this with a fresh temp dir every run; if you ever run the inspector another way, force a fresh index or your "proof" is a lie.
- **Everything the run writes stays OUT of the project.** The workdir holds the IDE scratch and the per-inspection XML, never the repo. If any landed inside, the scan would inspect its own logs and emit phantom findings (`LossyEncoding`, `HtmlUnknownTag`, `JsonStandardCompliance` citing the output dir) — any finding pointing into the workdir is self-inspection; discard it.
- **Most "noise" has a real source fix — apply it.** Don't pre-label a category "noise": an `A && B || die` guard becomes an explicit `if`, a dead link gets a real target, a `ptr` helper becomes `new(expr)`, ignored writes get `_, _ =` discards, `http://` test fixtures become `https`. Read each finding. What's left is a SHORT list of genuine tradeoffs — surface each with its cost. (A fix can backfire in a doc fence: modernizing a README snippet makes GoLand's isolated-fence analyzer emit *more* `Annotator` findings since it doesn't apply the current language level there; the snippet's correct, so keep it and surface the limitation.)

## Notes

Needs a licensed local GoLand and can't run in CI (no IDE on runners), so it's a deep local check, not a gate — `go vet` / `golangci-lint` stay the CI gates. It reads source, never modifies it.
