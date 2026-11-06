# Parity notes

The Go port reproduces `~/dev/fund` exactly. Golden vectors are generated from the web app's real
code (`make parity`) and replayed by `go test`. Quirks below were **preserved on purpose**; decide
separately whether to change them.

## Planning maths (`internal/plan`)
- `simulateFinishes` pays items **in input order** (position order), not by deadline, so deadline
  risks depend on item order.
- All arithmetic is floating point (not integer division); the UI rounds. `gap <= 0.5` decides
  `on-plan` vs `short`.
- `paydaysBetween` excludes the plan start date itself, so a plan that starts on a payday does not
  count that payday. It is capped at 480 dates; `finish`/risk walks are capped at 600 steps.
- Unknown timezones silently fall back to UTC for both the date and the clock hour.

## Formatting and export (`internal/money`, `internal/export`)
- `money()` uses the en-US narrow-symbol prefix per ISO code (table generated from Node's `Intl`).
  Unknown labels render as `<label> N,NNN`. A value that rounds to negative zero prints `-0`.
- CSV import accepts whatever JS `Number()` accepts as a whole number: `1e3`, `0x10`, `0b11`,
  `010` (=10, not octal), padded spaces. Rejects `1_000`, `Infinity`, empty, fractions.
- Go's JSON encoder escapes U+2028/U+2029 and replaces invalid UTF-8; `JSON.stringify` does not.
  Not covered by vectors; only matters for hand-crafted names.
- Zip output is "stored" like the web app but is not byte-identical to fflate's; file contents are.
