# Build specification, contract v1 (working proposal)

`SPEC.md` says Moldloft validates each build specification against
`CONTRACT_VERSION=v1` but does not define the fields. This page writes down
the shape Moldloft reads today so that it can be reviewed and corrected. It is
a proposal, not an agreed contract. Complete examples are in
[`stack-templates/calculator/fixtures/golden/`](../stack-templates/calculator/fixtures/golden).

## Rules for the whole file

- One JSON object, UTF-8, at most 1 MiB, nested at most 16 levels.
- A field the contract does not define is refused. So is a field name used
  twice in one object, and anything after the closing brace.
- These field names are refused anywhere in the file, because they mark raw
  observation material: `html`, `raw_html`, `screenshot`, `screenshots`,
  `snapshot`, `snapshots`, `observation`, `observations`.
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
| `item_id` | The pipeline item: lower-case letters, digits and hyphens. |
| `tool.slug`, `tool.title`, `tool.summary` | The generated tool's name (lower-case letters, digits, hyphens), title (80 characters) and summary (400). |
| `pattern` | Which stack template the tool needs. The matching rule of each template reads only this field. A pattern no built template matches is `E_NEEDS_SUBTOOL`. |
| `citations` | 1 to 50 of `{id, label, note?, url?}`. `url` must be a plain `https://` address and is shown as text, never loaded. |
| `scope_limits` | 1 to 20 lines saying what the tool does not cover. |
| `body` | The pattern-specific part, below. |
| `worked_examples` | 1 to 50 worked examples; their shape belongs to the pattern. |

## `body` for the `calculator` pattern

| Field | Meaning |
| --- | --- |
| `inputs` | 1 to 40. Each has `id`, `label`, `kind`, optional `help`. `kind: "number"` adds `decimals` (0 to 9), `min`, `max`, `default`, optional `unit`. `kind: "choice"` adds `table` and `default` (a row key); its value is the chosen row's value. Any other kind is `E_NEEDS_SUBTOOL`. |
| `constants` | Optional, up to 40 of `{id, label, value, unit?, citation?}`. |
| `tables` | Optional, up to 20 of `{id, label, unit?, citation?, rows}` with 1 to 200 rows of `{key, label, value}`. |
| `steps` | 1 to 80 of `{id, label, op, args, decimals, unit?, citation?}`. Each argument names an input, a constant or an earlier step. |
| `outputs` | 1 to 10 step ids shown as the result. |

Inputs, constants and steps share one set of ids.

| `op` | Arguments | Result before rounding |
| --- | --- | --- |
| `add` | 2 to 8 | the sum |
| `sub` | 2 | first minus second |
| `mul` | 2 to 8 | the product |
| `div` | 2 | first divided by second |
| `pct` | 2 | first times second, divided by 100 |
| `min`, `max` | 2 to 8 | the smallest, the largest |

Any other `op` is `E_NEEDS_SUBTOOL`. Arithmetic is exact decimal arithmetic.
Every step is rounded to its own `decimals`, halves away from zero. A result
of 10^15 or more is refused.

## Worked examples for the `calculator` pattern

Each is `{id, label, inputs, expect}`. `inputs` gives a value for every input
(a decimal string, or a row key for a choice). `expect` states the result of
every output, and of any other steps, written with exactly that step's number
of decimal places. Moldloft works each example out before generating anything.
If a stated result differs from the calculation, or an example cannot be
worked out, the specification is `E_PARSE`.
