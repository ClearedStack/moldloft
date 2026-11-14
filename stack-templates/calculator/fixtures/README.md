# Calculator fixtures

Saved inputs and the exact output expected for each. All data is made up.

- `golden/<name>/spec.json` is a build specification and
  `golden/<name>/expected/` is the repository it must produce, byte for byte.
- `rejects/<name>.json` is a build specification that must be refused and
  `rejects/<name>.want` is the exact error line. Each reject is the golden
  specification with one thing wrong; the file name says what.

After a deliberate change to the template, rewrite the expected output with

    go test ./stack-templates/calculator -update

and review the difference before committing it.
