# 0004: Refuse input it can't trust, and runs that checked nothing

## Context
A tool that reads an error page, a cut-off scrape or a failed render as "no problems" is worse than no tool.

## Decision
Exit 2 with nothing on stdout, whatever `--fail-on` says, when any scrape, rule file or dashboard can't be read in full, and when no selector could be checked. A scrape cut exactly between two metrics can't be seen in the text format, so a missing metric whose prefix matches others in the scrape is an error finding (`not-in-scrape`).

## Consequences
Some valid inputs are refused, such as a YAML rule file with no final line break. The message says what to fix. Tests feed every kind of bad input and cut the real files at every line and byte.
