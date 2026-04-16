---
name: unit-converter
description: Convert between common units using a multiplication factor. Use when asked to convert miles, kilometers, pounds, or kilograms.
---

## Usage

Use this skill when the user asks to convert between units.

1. Read `references/conversion-table.md` to find the factor for the requested conversion.
2. Run `scripts/convert.py` with `--value <number> --factor <factor>`.
3. Present the converted value clearly with both units.