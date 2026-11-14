# Build specification, contract v1 (working proposal)

`SPEC.md` says Moldloft validates each build specification against
`CONTRACT_VERSION=v1` and Muster's `contract/v1/CONTRACT.md` passes upstream
worker outputs in `results.waybill`. Because `CONTRACT.md` leaves the inner
contents of `results.waybill` opaque, this page writes down the exact JSON
shape Moldloft reads today so Waybill and Moldloft can align on one shared
schema. Complete examples live under `fixtures/golden/` in each folder of
[`stack-templates/`](../stack-templates).

## Envelope in `results.waybill`

When Moldloft leases an `approved` or `building` item from Muster, it ignores
`found` and every `results` entry except `results.waybill`. `results.waybill`
may be either:

1. The build specification object directly (carrying `contract_version`), or
2. An envelope object carrying catalog metadata alongside `"spec": { ... }`,
   from which Moldloft extracts `spec` after verifying that the outer envelope
   contains no forbidden raw observation fields.

## Rules for the whole file

- One JSON object, UTF-8, at most 1 MiB, nested at most 16 levels.
- A field the contract does not define is refused. So is a field name used
  twice in one object, and anything after the closing brace.
- These field names are refused anywhere in the file, because they mark raw
  observation material: `html`, `raw_html`, `raw_text`, `body_text`,
  `page_text`, `dom`, `headers`, `response_headers`, `screenshot`,
  `screenshots`, `snapshot`, `snapshots`, `observation`, `observations`.
- Display text is one line: no line breaks, control or invisible characters,
  no leading or trailing space, and nothing that looks like markup (`<` followed
  by a letter, `/`, `!` or `?`).
- An identifier is a lower-case letter followed by up to 31 lower-case letters,
  digits or underscores.
- A decimal number is written as a string: up to 15 digits, then optionally a
  point and up to 9 digits. No exponent, no separators. A count such as
  `decimals` is a JSON whole number.

Every breach of these rules is `E_PARSE`.

## Fields common to every pattern

| Field | Meaning |
| --- | --- |
| `contract_version` | Must be `"v1"`. |
| `item_id` | Muster item identifier (`PA-0001` through `PA-999999999`) or a local fixture slug (`item-sample-0001`). In worker mode, `item_id` must equal the leased grant's `id`. |
| `tool.slug`, `tool.title`, `tool.summary` | The generated tool's name (lower-case letters, digits, hyphens), title (80 characters) and summary (400). |
| `pattern` | Which stack template the tool needs (`calculator`, `checklist`, `validator`, or `decision-tree`). A pattern no built template matches is `E_NEEDS_SUBTOOL`. |
| `citations` | 1 to 50 of `{id, label, note?, url?}`. `url` must be a plain `https://` address and is shown as text, never loaded. |
| `scope_limits` | 1 to 20 lines saying what the tool does not cover. |
| `body` | The pattern-specific part, below. |
| `worked_examples` | 1 to 50 worked examples; their shape belongs to the pattern. |

## `calculator` pattern

- `body`: `inputs` (1 to 40 `number` or `choice` inputs; any other `kind` such as `date` is `E_NEEDS_SUBTOOL`), optional `constants`, optional `tables`, `steps` (1 to 80 exact-decimal steps using `add`, `sub`, `mul`, `div`, `pct`, `min`, `max`, rounded half-away-from-zero to `decimals`), and `outputs` (1 to 10 step ids).
- `worked_examples`: `{id, label, inputs, expect}` verified against the exact decimal evaluator before any file is generated.

## `checklist` pattern

- `body`: `items` (1 to 60 of `{id, label, required, allow_na, citation, help?, kind?}`). Only `kind: "yes_no"` (the default) is built; any other `kind` is `E_NEEDS_SUBTOOL`.
- `worked_examples`: `{id, label, answers, expect}` where `expect` states `{status, unmet_required, yes_count, no_count, na_count}`. `status` is `"complete"` when every required item is answered `"yes"` (or `"na"` when `allow_na` is true) and `"incomplete"` otherwise.

## `validator` pattern

- `body`: `delimiter` (`comma`, `pipe`, `colon`, or `tab`; any other value is `E_NEEDS_SUBTOOL`), `has_header` (boolean), optional `tables` (`{id, label, citation?, codes}`), and `columns` (1 to 20 of `{id, label, required, rule, citation, min_len?, max_len?, table?, prefix?}`). Built column rules are `text`, `digits`, `upper_code`, and `in_table`; any other `rule` is `E_NEEDS_SUBTOOL`.
- `worked_examples`: `{id, label, input, expect}` where `input` is multiline delimited text and `expect` states `{valid, row_count, error_count}`.

## `decision-tree` pattern

- `body`: `start` (starting question id), `questions` (1 to 40 of `{id, prompt, help?, citation, kind?, options}` with 2 to 10 `{key, label, next}` options; only `kind: "choice"` is built), and `outcomes` (1 to 20 of `{id, title, summary, status, citation}` where `status` is `eligible`, `ineligible`, or `review`). Cycles, dangling `next` targets, and unreachable questions or outcomes are refused with `E_PARSE`.
- `worked_examples`: `{id, label, answers, expect}` where `expect` states `{outcome, status, steps}`.
