# Working on glean

## Tooling and verification

Use mise's pinned tools. Run `mise run fix` when corrections are needed, then `mise run check` after changes. Fix modifies files; check verifies formatting, lint, module tidiness and applicable race tests. Neither stages files. `mise run test` runs race tests independently. Resolve failures and report any checks that could not run.

## Go conventions

- Keep the configured function ordering; do not reorder functions arbitrarily.
- Parallelize tests with isolated state. Tests needing process-wide environment, cwd or shared native installations must remain serial, with a narrow explained linter exemption when needed.
- CLI and TUI call shared application operations. Keep business logic out of presentation handlers and storage encoding out of domain code.
