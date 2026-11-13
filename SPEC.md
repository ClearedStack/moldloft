# Moldloft specification

> Technical specification for the deterministic Moldloft worker that generates isolated MIT static tool repositories from clean-room JSON specs and stack templates.

**Status: design.** This is a specification. It describes how Moldloft is meant to work, not something that exists yet.

The same text is published at [clearedstack.com/platform/moldloft/](https://clearedstack.com/platform/moldloft/). To change the specification, change it here and say why.

## Overview & Role

Moldloft (`v0.1.0-draft`) is the deterministic clean-room build worker in ClearedStack v3.1, replacing the original Stage 8 (Build). Implemented in the Moldloft repository under the MIT License, it reads sanitized JSON build specs and worked-example fixtures compiled by Waybill and generates isolated, MIT-licensed static tool repositories inside Plainforge.

Zero observation artifacts ever enter Moldloft, and zero AI models run during code generation. Every generated tool is pure static HTML, CSS, JavaScript, and bundled JSON reference tables that run entirely in browser memory with no backend server and no external CDN dependencies.

## Runtime & Resource Budget

Moldloft runs in the `cs-workers` Kubernetes namespace under the **Standard (Workers)** priority class.

| Attribute | Value |
| --- | --- |
| **Worker ID / Repo** | Moldloft (`moldloft`) |
| **Namespace** | `cs-workers` |
| **Kind / Version** | `worker` · `v0.1.0-draft` |
| **RAM Budget** | `30 MiB` (always-on Go pod) |
| **Runtime AI Calls** | `0` (100% deterministic `stack-templates/` code generation) |
| **Local Spool** | `/var/spool/cs` (buffers generated repo metadata if Muster restarts) |
| **Build & Deploy** | Built with `ko` (no Docker) by Keelworks, signed with `cosign` in Lazarette, deployed by Shipkeeper via Server-Side Apply |

## Interfaces & State Transitions

Moldloft leases work from Muster in the `clearedstack` namespace over ServiceAccount-authenticated HTTP (`CONTRACT_VERSION=v1`), draining `urgent` → `normal` → `backfill` priority lanes.

- **Inbound state:** `building` (clean-room JSON build spec + worked-example fixtures emitted by Waybill, or regulatory drift re-builds routed back from Tidegauge).
- **Build handoff:**
  1. Validates the clean-room JSON spec against `CONTRACT_VERSION=v1`.
  2. Selects the matching deterministic stack template under `stack-templates/`.
  3. Generates the isolated static tool repository (including `LICENSE` under MIT, bundled lookup tables, strict CSP headers `default-src 'self'; connect-src 'self'; form-action 'none'`, regulatory citations, scope limits, and worked-example tests) and routes it to Plainforge (mirrored to GitHub on change, with zero container images pushed to GitHub).
  4. Hands off for Keelworks rootless BuildKit packaging and Shakedown 5-condition automated QA.
- **Outbound transitions:**
  - Remains in `building` until Shakedown passes all five listing conditions and advances to `live` via Chartroom.
  - `parked` — Moved to parked if blocked by unresolved regulatory ambiguity.
  - `needs_subtool` — Fail-closed pause (`E_NEEDS_SUBTOOL`) when the spec requires an interaction pattern not supported by existing `stack-templates/`.

## Sub-Tools & Failure Modes

Code-generation patterns live inside Moldloft as deterministic stack templates under `stack-templates/`:

| Sub-Tool (Stack Template) | Best For | Generated Inputs & Outputs |
| --- | --- | --- |
| `calculator` | Rate build-ups, interest math, statutory thresholds | Numeric rates or dates in; step-by-step math and cited totals out. |
| `checklist` | Solicitation reviews, compliance preparation | Structured yes/no items with regulatory citations and optional local browser state. |
| `validator` | Checking codes, tables, or local files | User-supplied text or local files parsed in browser memory against bundled rules. |
| `decision-tree` | Eligibility, clause applicability, routing | Guided branching questions leading to a deterministic, cited outcome. |

### Sanitized Failure Modes

| Error Code | Trigger | Platform Behavior |
| --- | --- | --- |
| `E_NEEDS_SUBTOOL` | Clean-room spec requires an interaction pattern unsupported by `calculator`, `checklist`, `validator`, or `decision-tree`. | Fails closed to `needs_subtool`. Bellbuoy opens a deduplicated task in Moldloft on Plainforge; once a new template and fixture deploy, Muster auto-replays paused items. |
| `E_PARSE` | Clean-room spec fails contract validation or contains forbidden raw observation fields. | Rejected at Muster / Moldloft before generation starts; fingerprinted error sent via Bellbuoy. |
| `E_TIMEOUT` | Transient timeout reporting build completion to Muster. | Buffered in `/var/spool/cs` and retried until acknowledged. |

## Security & Clean-Room Boundaries

- **Absolute clean-room isolation:** Muster enforces the shared contract boundary by rejecting any payload containing raw Sextant HTML, screenshots, or snapshots before Moldloft can lease it. Moldloft never accesses vendor sites or observation archives.
- **Synthetic fixtures only:** Worked-example test fixtures must use hand-calculated official examples, boundary cases, and synthetic data. Real CUI, proprietary contractor rates, and non-public solicitation text are strictly forbidden.
- **Generated tool security:** Every generated static tool enforces `connect-src 'self'`, `default-src 'self'`, `form-action 'none'`, zero tracking/analytics, and offline execution.
- **Pod hardening & network policy:** Runs non-root with dropped capabilities, read-only root filesystem (except `/var/spool/cs`), zero database credentials, and default-deny `NetworkPolicy` permitting only ServiceAccount HTTP to Muster (no direct access to Logbook, Plainforge, or Lazarette).

## Related pages

- [The pipeline and its ten workers](https://clearedstack.com/platform/pipeline/#building-verifying-and-listing)
- [Clean room and credit](https://clearedstack.com/platform/clean-room-and-credit/)
- [An item’s life: fourteen states and three lanes](https://clearedstack.com/platform/item-lifecycle/)
- [The pipeline and its ten workers](https://clearedstack.com/platform/pipeline/)
