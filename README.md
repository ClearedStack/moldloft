<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/logo-dark.svg">
    <img src="brand/logo-light.svg" alt="Moldloft" height="56">
  </picture>
</p>

<p align="center">
  <strong>Generates a tool's repository from its specification and a fixed template.</strong><br>
  Part of <a href="https://clearedstack.com">ClearedStack</a>
</p>

---

> **Status: ready to deploy.** All four stack templates (`calculator`, `checklist`, `validator`, `decision-tree`), the Muster contract v1 worker loop, the `/var/spool/cs` spool, the Plainforge smart HTTP Git committer and stand-ins, and the `cs-workers` Kubernetes manifests are built and tested with the Go standard library only. See [Open cross-repo items](#open-cross-repo-items) before live pipeline runs.

Moldloft is stage 06 of the ClearedStack pipeline. ClearedStack is an open pipeline that finds ideas for government-contracting tools, screens and scores them, and builds each one from the public rule. Moldloft can also run standalone without the cluster.

## What works today

Generate a static tool folder directly from a build specification:

    go run ./cmd/moldloft generate -spec stack-templates/calculator/fixtures/golden/sample-rate-buildup/spec.json -out /tmp/sample-rate-buildup

Run a standalone worker pass against in-memory Muster and Plainforge stand-ins:

    go run ./cmd/moldloft run -standalone -once -spool /tmp/moldloft-spool

Run the cluster worker against Muster:

    MUSTER_URL=http://muster.clearedstack.svc.cluster.local:8080 ./moldloft run

- **Four deterministic stack templates.**
  - `calculator`: exact decimal arithmetic (`add`, `sub`, `mul`, `div`, `pct`, `min`, `max`), half-away-from-zero rounding, lookup tables, constants, step-by-step citations.
  - `checklist`: yes/no/not-applicable compliance checklists with required vs advisory items, unmet-requirement counts, and regulatory citations.
  - `validator`: browser-memory delimited record and code-table validators (`comma`, `pipe`, `colon`, `tab`; `text`, `digits`, `upper_code`, `in_table` column rules) with local file reading via `FileReader` and zero persistence.
  - `decision-tree`: acyclic branching questionnaires verified at build time for reachability and absence of cycles, routing to cited `eligible`, `ineligible`, or `review` determinations.
- **Byte-for-byte reproducibility.** Generated file trees and Git blob/tree/commit packfiles use fixed modes and epoch timestamps, reading no wall clock, randomness, hostname, or network resource.
- **Self-contained static output.** Every generated repository carries `index.html`, local CSS and JavaScript, bundled JSON data, strict CSP (`default-src 'self'; connect-src 'self'; form-action 'none'`), MIT `LICENSE`, `README.md` with scope limits and citations, runnable worked examples (`tests/index.html` and `node tests/run.js`), and `moldloft.json` with per-file SHA-256 hashes.
- **Muster contract v1 worker (`internal/muster`, `internal/worker`).** Speaks `POST /v1/lease`, `/v1/heartbeat`, `/v1/complete`, `/v1/fail`, and `/v1/needs_subtool` with bearer ServiceAccount authentication, 64 KiB request bounds, sanitized error/log handling, `/var/spool/cs` crash-safe outcome staging, and replay tests against all 52 saved examples from `ClearedStack/muster/contract/v1/examples/`.
- **Plainforge Git committer (`internal/plainforge`).** Builds deterministic Git packv2 streams in memory and pushes initial or fast-forward commits over Git's smart HTTP `git-receive-pack` protocol without shelling out to `git`, falling back to `DryCommitter` when `PLAINFORGE_URL` is unset.
- **Cluster deployment (`deploy/`, `.ko.yaml`).** `CGO_ENABLED=0` `ko` build config, hardened `cs-workers` `Deployment` (`runAsNonRoot`, `readOnlyRootFilesystem`, `drop: ["ALL"]`, `RuntimeDefault` seccomp, `24Mi`/`30Mi` memory budget, `arm64` node affinity excluding `cm3588-plus`, image pinned by `@sha256` digest), and default-deny `NetworkPolicy` opening only DNS and Muster.

### Tests

    go vet ./...
    go test -race ./...

The suite replays all 52 saved Muster contract v1 examples, checks every built stack template against golden trees and Node.js worked-example runners, verifies `git index-pack --strict` and `git fsck --strict` on generated packfiles, exercises worker priority lanes, rebuilds, clean-room isolation, and `/var/spool/cs` recovery across Muster `503` outages, and validates the manifests in `deploy/`.

### Open cross-repo items

1. **Waybill build-specification schema.** Muster's `contract/v1/CONTRACT.md` passes Waybill's output in `results.waybill` without defining its inner JSON fields. `ClearedStack/waybill` currently emits `{provenance, authority, schema, vectors}` for simple formulas without rounding modes, scope limits, or non-calculator patterns, whereas Moldloft validates [`docs/build-spec-v1.md`](docs/build-spec-v1.md) (accepting either the direct spec or `{"spec": {...}}` in `results.waybill`). Waybill needs to emit or wrap `docs/build-spec-v1.md` before live `waybill -> moldloft` handoffs succeed.
2. **Plainforge network path vs `SPEC.md` `NetworkPolicy`.** `SPEC.md` states both that Moldloft commits generated repositories to Plainforge over Git smart HTTP and that `cs-workers` `NetworkPolicy` allows egress only to Muster (with no direct access to Plainforge). `deploy/networkpolicy.yaml` enforces the stricter Muster-only rule and `moldloft run` uses `DryCommitter` until `PLAINFORGE_URL` and an explicit Plainforge egress rule (or control-plane relay) are added when Plainforge is deployed.
3. **Pausing rebuilds (`building -> needs_subtool`) and `parked`.** `SPEC.md` mentions `parked` and `needs_subtool`, while Muster's `CONTRACT.md` assigns `approved -> building` and `building -> building` to Moldloft (`parked` belongs only to Countersign and Shakedown, and `needs_subtool` is permitted from `approved` but refused with `409 move_not_allowed` from `building`). Moldloft follows `CONTRACT.md` strictly; if a revised spec on a `building` item hits `E_NEEDS_SUBTOOL`, Muster should either allow `building -> needs_subtool` in a future contract revision or route rebuild pauses through `fail`.
4. **Placeholder image digest before Lazarette deploy.** `deploy/moldloft.yaml` pins `lazarette.clearedstack.svc.cluster.local:5000/moldloft@sha256:000...000`; replace the all-zero digest with the `ko build --platform=linux/arm64` image digest once Lazarette is running.
5. **Calculator date inputs.** Date inputs are not yet implemented in `stack-templates/calculator`; specifications requesting `kind: "date"` return `E_NEEDS_SUBTOOL`.

## In this repository

| File | What it is |
| --- | --- |
| [`SPEC.md`](SPEC.md) | The specification: what Moldloft does, its interfaces, how it fails and how it is kept safe. |
| [`PROMPT.md`](PROMPT.md) | The prompt to start a build session with. |
| [`component.json`](component.json) | This component's entry in the ClearedStack naming registry. |
| [`.ko.yaml`](.ko.yaml) | Static `CGO_ENABLED=0` `arm64` image build configuration for `ko`. |
| [`deploy/`](deploy) | Kubernetes `ServiceAccount`, `Deployment`, and `NetworkPolicy` manifests for `cs-workers`. |
| [`cmd/moldloft/`](cmd/moldloft) | CLI entrypoint (`moldloft run`, `moldloft generate`, `moldloft version`). |
| [`stack-templates/`](stack-templates) | Registry and implementations of `calculator`, `checklist`, `validator`, and `decision-tree`. |
| [`internal/`](internal) | Specification parser, exact decimal math, Muster contract v1 client and stand-in, Plainforge packfile/HTTP client and stand-in, `/var/spool/cs` spool, and worker loop. |
| [`testdata/contract/v1/examples/`](testdata/contract/v1/examples) | Copy of Muster's 52 saved contract v1 examples replayed by `internal/muster`. |
| [`docs/build-spec-v1.md`](docs/build-spec-v1.md) | Working schema for `results.waybill` build specifications. |

## At a glance

| | |
| --- | --- |
| **Stage** | 06 of 10 in the pipeline |
| **Formerly** | `cs-06-build`, then Jig |
| **Namespace** | `cs-workers` |
| **Container image** | `lazarette.clearedstack.svc.cluster.local:5000/moldloft` |
| **Memory budget** | 30 MiB |
| **Depends on** | [Muster](https://github.com/ClearedStack/muster), [Plainforge](https://github.com/ClearedStack/plainforge) |
| **Depended on by** | Nothing else in the platform |
| **Rules for particular cases** | `calculator`, `checklist`, `validator`, `decision-tree` |

## License

[MIT](LICENSE). ClearedStack is an independent personal project. It is not affiliated with, endorsed by, or sponsored by the U.S. Department of Defense or any other government agency.
