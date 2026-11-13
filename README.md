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

> **Status: design.** One part of the specification is built: the `calculator` stack template and a command that runs it. The worker itself is not built. See [What works today](#what-works-today).

Moldloft is stage 06 of the ClearedStack pipeline. ClearedStack is an open pipeline that finds ideas for government-contracting tools, screens and scores them, and builds each one from the public rule. Moldloft is meant to be understood and used without the rest.

## What works today

One command turns one build specification into a folder of static files:

    go run ./cmd/moldloft generate -spec stack-templates/calculator/fixtures/golden/sample-rate-buildup/spec.json -out /tmp/sample-rate-buildup

Open `index.html` in that folder to use the generated calculator. The command needs Go 1.24 or later and nothing else: no network, no dependencies outside the standard library.

- **The `calculator` template.** Number and pick-from-a-table inputs, fixed values, lookup tables, and steps that add, subtract, multiply, divide, take a percentage, or take the smallest or largest. Each step is shown with its wording, its result and its citation. The arithmetic is exact decimal arithmetic, not floating point.
- **The same bytes every run.** The output depends only on the bytes of the specification. Nothing reads a clock, a machine name or the network.
- **What a generated folder holds.** `index.html`, CSS and JavaScript with no outside resources, the bundled data as JSON, the content security policy `default-src 'self'; connect-src 'self'; form-action 'none'` on every page, an MIT `LICENSE`, a README with the citations and scope limits, the worked examples with two ways to run them (a page, and `node tests/run.js`), and `moldloft.json` listing the SHA-256 of every file.
- **Worked examples are checked first.** If a specification's worked example does not match the calculation, nothing is generated.
- **Refusals.** A broken specification is refused with `E_PARSE`. A valid one that needs a pattern, an input kind or an operation with no rule is refused with `E_NEEDS_SUBTOOL`. Either way nothing is written, the command exits with 1, and the error names a field in the specification and never a value, path or machine.

The specification does not define the fields of a build specification, so the shape this command reads is a proposal, written out in [`docs/build-spec-v1.md`](docs/build-spec-v1.md).

### Tests

    go test ./...

The tests use no network and only made-up data. They include the saved examples in [`stack-templates/calculator/fixtures/`](stack-templates/calculator/fixtures): two specifications with the exact folder each must produce, and 44 specifications that must be refused with the exact error line for each. Two tests run the generated JavaScript with Node.js to prove it agrees with the Go arithmetic; without Node.js they are reported as skipped.

### Not built yet

Everything else in [`SPEC.md`](SPEC.md):

- The worker: leasing from Muster, the `urgent`, `normal` and `backfill` lanes, and the `building`, `parked` and `needs_subtool` state changes.
- Committing the generated repository to Plainforge.
- The local spool in `/var/spool/cs`, retrying a completion report, and `E_TIMEOUT`.
- The in-memory stand-ins for Muster and Plainforge.
- The `checklist`, `validator` and `decision-tree` templates. A specification asking for one is refused with `E_NEEDS_SUBTOOL`.
- Dates as calculator inputs. The specification mentions them; only numbers are built.
- The `ko` image build, the Kubernetes workload, its hardening and network policy, and any check of the 30 MiB memory budget inside a pod.

## In this repository

| File | What it is |
| --- | --- |
| [`SPEC.md`](SPEC.md) | The specification: what Moldloft does, its interfaces, how it fails and how it is kept safe. |
| [`PROMPT.md`](PROMPT.md) | The prompt to start a build session with. It says what to build and the rules to build it under. |
| [`component.json`](component.json) | This component’s entry in the ClearedStack naming registry. |
| [`brand/`](brand) | The Moldloft logo and mark, for light and dark backgrounds. |
| [`cmd/moldloft/`](cmd/moldloft) | The command. |
| [`stack-templates/`](stack-templates) | The registry of stack templates and one folder for each template that is built: its matching rule, its logic and its fixtures. |
| [`internal/`](internal) | Reading a build specification, exact decimal arithmetic, error codes, and writing the generated folder. |
| [`docs/build-spec-v1.md`](docs/build-spec-v1.md) | The proposed shape of a build specification. |

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

### Boundaries

- Leases build specifications from Muster
- Commits the generated repository to Plainforge

## Names

`moldloft` is this component’s identifier everywhere: the repository, the workload, the container image. The one place names are written down is the [naming registry](https://clearedstack.com/platform/registry.json), kept in [`clearedstack.com`](https://github.com/ClearedStack/clearedstack.com/blob/main/src/data/registry.json). `component.json` here is a copy of this component’s entry. If the two disagree, the registry is right.

## Ground rules

- **No AI when it runs.** The same input gives the same output on any machine.
- **Stop instead of guessing.** A case with no rule is paused and reported, never improvised.
- **Clean room.** Written from scratch. No code, text or design is copied from another project.
- **Public sources and made-up data only.** Nothing classified, controlled or taken from a real contract, in code, tests, issues or commit messages.

## License

[MIT](LICENSE). ClearedStack is an independent personal project. It is not affiliated with, endorsed by, or sponsored by the U.S. Department of Defense or any other government agency.
