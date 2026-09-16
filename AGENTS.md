# Tooling

- Use the tools pinned in `mise.toml` and `mise.lock`: run commands through
  `mise exec --`. Run `mise install` if those tools are missing.
- Before finishing Go changes, run `mise exec -- golangci-lint fmt`,
  `mise exec -- go test ./...`, and `mise exec -- golangci-lint run`.
  `.golangci.yml` defines the formatting and lint rules; fix issues without
  weakening the configuration or adding blanket suppressions.
- GoReleaser builds release binaries and archives using `.goreleaser.yaml`.
  Run `mise exec -- goreleaser check` after changing release configuration.
  Use `mise exec -- goreleaser release --snapshot --clean` for a local release
  rehearsal without publishing.

# Go conventions

- Wrap propagated errors with operation context and `%w`. Check close errors,
  preserve earlier errors, and make ownership explicit when libraries close streams.
- Use `t.Context()` in tests and keep `os.Exit` outside functions with deferred cleanup.
- Use `slog` with `tint` for CLI logs on stderr, with structured attributes.
  Keep stdout for query results and plan JSON; log errors once at the CLI boundary.
