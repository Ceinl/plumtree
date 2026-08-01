# Consolidation baseline

Issue [#52](https://github.com/Ceinl/plumtree/issues/52) freezes the safety
net for replaying the clean-break consolidation. The observable baseline is
`origin/main` commit `9ac60460f3ed65ce98f0ef23276c0e0278875c50`.
Production implementation remains unchanged in this guardrail step.

The preserved prototype, including branch `worktree-consolidation-compat`, is
inventory and code reference only. It is not a merge source. In particular,
prototype commit `9becf08` identifies the exact ABI-v4 artifact frozen here and
`c3919ad` demonstrates the later self-contained SDK move; accepted work is
recreated from the baseline in the ordered issue series.

## Baseline inventory

| Current ownership | Retained use case | Planned change |
| --- | --- | --- |
| `sdk` and `tui-runtime` | Native and hosted interactive/finite Go leaves, capabilities, ABI v4 | Make the SDK self-contained in #53; select the redesigned SDK in #62 |
| `runner` | In-process and isolated WASI execution with bounded explicit capabilities | Move neutral runner ownership in #54; implement the clean ABI in #61 |
| `ssh-gateway` | Authenticated SSH leaf sessions and host-owned terminal rendering | Move gateway ownership in #55; replace transport and identity in #69 |
| `control-plane` | App, artifact, session, secret, egress, identity, and persistence behavior | Move ownership in #56; select the replacement state/API at #73 |
| `pt` and `build-worker` | Project creation, local development, deployment, and management | Move the retained client/build code in #57; remove server source builds at #73 |
| examples and `_devtest` | Public-SDK consumer and capability scenarios | Keep coverage until every consumer moves to the clean SDK in #62 |
| native and Compose assets | Local all-in-one and isolated production operation | Replace as one coherent cutover in #73 and qualify in #74 |

`testdata/consolidation/journeys.tsv` maps retained journeys to concrete fixtures
and proof tests. `external-consumers.txt` lists standalone modules compiled as
public SDK consumers. A fixture cannot disappear while its journey claim still
points to it.

## Ratchets

`dependencies.tsv` is an exact allowlist of the non-standard module dependency
set observed for every current workspace module, including test dependencies.
A dependency addition or removal fails until the manifest is reviewed. Removing
a dependency should reduce the allowlist in the same pull request; new reverse
or implementation edges must not be allowlisted merely to make CI pass.

`removals.tsv` records paths and terms scheduled for deletion. Counts are exact:
additions fail, and intentional removals require lowering the recorded count in
the same pull request. Consolidation inventory documentation and guardrail data
are excluded from textual debt counts so describing a removal does not recreate
that product surface.

Known staging debt is deliberately recorded rather than fixed here. This
includes the standalone `tui-runtime`, sibling-module replacements, old product
module paths, source-build flow, Shoo/dashboard/claim surfaces, old state and
token concepts, legacy server binaries, and the old action protocol.

## Frozen behavior and security floor

The checked-in `runner/testdata/compat/abi-v4-counter.wasm.gz` is an already-built
ABI-v4 guest from the baseline. Its exact uncompressed bytes are pinned by size
and SHA-256 and run through both current hosted runner compositions. It is not
regenerated when the SDK or runner changes.

Focused black-box journeys retain these boundaries while ownership moves:

- structured guest cells and untrusted CLI text cannot inject terminal controls;
- the in-process and isolated runners execute the frozen guest equivalently;
- the worker rejects the wrong control credential;
- isolated capability proxying does not gain ungranted authority;
- invalid limits fail rather than becoming unbounded;
- HTTP lifecycle logs redact credentials;
- secret operations remain authorized; and
- SSH proof failures do not become authenticated identities.

The guardrail PR changes no persistent state, credential format, network
listener, authorization decision, production package, or user-facing command.
It adds reproducible evidence only.

## Local verification

Run all focused guardrails with:

```sh
./scripts/check-consolidation-guardrails.sh
```

The normal workspace vet, test, race, and release-contract jobs remain required.
For this one guardrail PR, review also verifies that the diff against its
`origin/main` base contains only CI, scripts, tests, fixtures, and inventory
documentation. Later consolidation PRs intentionally change production code, so
that one-time scope assertion is not installed as a permanent repository rule.
