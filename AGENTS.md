# Contributing to catchup

## Complexity must earn its cost

- Optimize the complexity-to-benefit ratio. Explain the concrete user benefit and why existing composition is insufficient.
- Be very conservative with added lines, including tests, fixtures, and documentation. A new provider does not justify unrelated infrastructure.
- When the benefit is uncertain, compare against removing or simplifying the mechanism. Show which behavior or measured performance suffers.
- Discuss substantial growth with the maintainer before implementation. Propose shared frameworks and cross-provider redesigns separately from provider additions.
- Report additions and deletions separately for runtime code, tests/fixtures, and docs. Unrelated deletions do not pay for new complexity.

For changes exceeding 300 lines, apply the [complexity guide](https://github.com/wilbeibi/wilbeibi-skills/blob/main/skills/code-review/COMPLEXITY.md).
Count additions plus deletions across the whole PR, including tests, fixtures, and docs.
Reduce maintenance cost, not readability or required behavior.

## Preserve the existing boundaries

- Adding a provider must not change existing providers' behavior. Verify shared-code changes preserve their behavior. Propose intentional changes separately.
- Keep format-specific behavior in its provider. Reuse common CLI selection and rendering instead of adding provider-specific alternatives.
- Reuse `session.Failure`, query/summary helpers, root resolution, and `internal/sqlitedb` before adding equivalents.
- Keep session reads read-only and compatible with concurrent writers. Use the shared SQLite opener to preserve WAL visibility.
- Normalize failures, stop events, and compaction into existing entry kinds. Preserve retained context through `Retained`.
- Preserve real user turns. Filter injected context using format evidence, and keep subagent metadata from replacing main-session metadata.

## Provider evidence

- Base parsers and fixtures on real session files or upstream schemas/source. Record the version, source, and unverified assumptions.
- Identify the authoritative transcript. Report material fallback losses through existing warnings, including lost pre-compaction history.
- Preserve relevant record shapes and ordering in sanitized fixtures. A fixture invented from the parser cannot establish compatibility.
- Verify native launch flags against the agent. For unsupported launch modes, return an actionable error instead of inventing a command.

## Tests must earn their place

Follow the [test-writing guide](https://github.com/wilbeibi/wilbeibi-skills/blob/main/skills/test-writing/SKILL.md).

- Prefer another fixture row over another harness or test file.
- Derive expected results independently. For JSON contracts, assert wire keys independently of production serialization types.
- Make the test fail by reverting the fix or breaking the protected behavior.
  If an existing test catches the same break on the same input, extend it instead of keeping a duplicate.
- Keep another test layer only for a distinct risk, such as CLI wiring versus parser semantics.
- Discard exploratory tests unless they protect a distinct contract or reproduced bug.
- Check behavior directly instead of scanning source for helper calls or requiring phrases in comments.

## Repository caveat

`docs/` is ignored except release notes. Verify new shipped documentation is tracked before linking to it.
