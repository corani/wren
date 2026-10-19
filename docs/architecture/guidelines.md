# Engineering guidelines

These guidelines follow the conventions established in bifrost, turnstile,
muffin, anansi, and rup, adapted to Wren's single-user web application.
See the [v1 design and implementation plan](../plans/0001/README.md) for product
behavior and scope.

## Structure and dependencies

- Put the executable in `cmd/wren/main.go` and application packages under
  `internal/`, grouped by responsibility: configuration, storage, vocabulary,
  study logic, and HTTP handling.
- Keep `main` focused on command setup, dependency wiring, resource ownership,
  and process lifecycle. Do not put business logic in command callbacks.
- Keep web assets in `web/` and vocabulary JSON in `data/`. Embed both from a
  small root package so embed paths never require `..`. Embed SQL migrations
  alongside the storage package.
- Wire dependencies explicitly through constructors such as `store.Open(...)`
  and `server.New(store, logger)`. Avoid service locators and mutable global
  dependencies.
- Prefer concrete types. Introduce interfaces where substitution is genuinely
  needed, such as a useful test boundary or multiple implementations. Define
  interfaces in the consuming package and include only the methods it needs.
  Do not create an interface for every struct.
- Prefer standard-library infrastructure: `net/http`, method-aware
  `http.ServeMux`, `encoding/json`, and `database/sql`. Do not add an HTTP framework,
  ORM, or generic repository layer without a concrete need.
- Keep packages and abstractions proportional to the application. Do not create
  empty packages or speculative extension points.

## Command-line interface and configuration

- Use Cobra for command-line handling. Keep the command surface small; do not
  copy unrelated subcommands from other projects.
- Return command failures through `RunE`. Print the final error and choose the
  exit status at the executable boundary; do not call `os.Exit` in internal
  packages. Avoid duplicate Cobra usage/error output for runtime failures.
- Use typed configuration with explicit defaults and separate loading/validation
  from application logic.
- Follow XDG conventions for configuration and application data locations, with
  explicit overrides where needed.
- Do not require a configuration file unnecessarily. If YAML configuration is
  needed, prefer `github.com/goccy/go-yaml` and strict decoding that rejects
  unknown fields.

## Logging and errors

- Use `log/slog` and inject a `*slog.Logger` where logging is needed. Use
  `charm.land/log/v2` as the handler for readable terminal output.
- Prefer short messages with structured fields, for example
  `log.Error("save answer", "card_id", id, "err", err)`.
- Return contextual errors while preserving the underlying error:
  `fmt.Errorf("store: update card: %w", err)`.
- Use `errors.Is` and `errors.As` to classify errors. Define sentinel or typed
  errors when callers need to distinguish outcomes; do not make callers parse
  error strings.
- Propagate errors until the layer responsible for handling or reporting them.
  Avoid logging the same failure at every layer.
- Surface failures explicitly. Do not turn failed operations into empty results,
  successful responses, or silent fallback behavior.

## Context and resource lifecycle

- Pass `context.Context` explicitly, as the first parameter, to operations that
  perform I/O. Use context-aware SQL, HTTP, and other blocking APIs.
- Establish application cancellation at the executable boundary, propagate
  request contexts, and shut down the HTTP server gracefully.
- Make resource ownership visible. Pair successful opens with cleanup for
  databases, files, response bodies, and servers.
- Report meaningful cleanup failures. Use narrowly scoped `//nolint:errcheck`
  exceptions only when discarding the error is intentional and justified; do
  not use blanket suppressions.

## Storage

- Use `database/sql` with `modernc.org/sqlite` and handwritten, parameterized SQL
  behind a concrete store type.
- Enable WAL, foreign keys on every connection, and a busy timeout.
- Use ordered, embedded SQL migrations for schema changes. Keep vocabulary sync
  separate from schema migration.
- Make multi-step state changes transactional. In particular, an answer's
  scheduler state, history entry, queue changes, and session revision must
  commit together.
- Preserve stable vocabulary identities and learning history during sync.
  Do not replace rows in ways that reset progress or delete dependent history.

## Testing

- **Prefer table-driven tests with named `t.Run` subtests over many small test
  functions for related behavior.** Group input variations, boundary cases,
  success cases, and failure cases under the operation they exercise.
- Use separate test functions for substantially different behavior or setup;
  do not force unrelated workflows into a complicated table.
- Use Go's `testing` package with Testify: `require` for prerequisites that must
  succeed before continuing, and `assert` for independent checks.
- Mark fixture helpers with `t.Helper()`. Use `t.TempDir()` and `t.Cleanup()` to
  isolate and release resources.
- Test persistence behavior against real temporary SQLite databases rather than
  mocking SQL calls or transaction semantics.
- Use `httptest` for HTTP handlers. Small hand-written fakes are appropriate at
  meaningful handler boundaries; avoid unnecessary mocking frameworks.
- Use deterministic clocks and fixtures for scheduling and queue behavior.
  Tests must not depend on external services or the developer's live data.
- Use `t.Parallel()` only for tests whose resources and state are isolated.
- Test observable behavior and important failure paths, not implementation
  details, trivial accessors, or framework behavior.

## Formatting and tooling

- Format Go code with gofumpt.
- Use the root `build.sh` as the standard entry point for formatting, testing,
  vetting, linting, building, and installation. It runs from the repository root
  regardless of the caller's working directory.
- Keep gofumpt and golangci-lint pinned using the `tool` directive in `go.mod`.
  The script invokes them through `go tool`; do not depend on global
  installations.
- Run focused tests while implementing where needed, then use the script for
  the relevant broader checks before considering the change complete:

  ```sh
  ./build.sh -g -t -v -l -b
  ```

- Actions also have word aliases: `gen`, `test`, `vet`, `lint`, `build`, and
  `install`. `-g` runs `go fix` followed by gofumpt; `-b` writes `bin/wren`;
  `-i` installs the executable. Run `./build.sh -h` for help. Multiple actions
  run in argument order; a failed action leaves the script's exit status nonzero.
- Build and install target `cmd/wren`; build outputs go to `bin/`. The script
  does not generate application code.
- Use the Go version declared by the module and keep module manifests and sums
  consistent with imported dependencies.
- Write concise comments for contracts, intent, or non-obvious behavior. Do not
  narrate straightforward code.

## Web assets and delivery

- Use semantic HTML, custom CSS, and native JavaScript modules. No Node build,
  frontend framework, bundler, transpiler, or CDN is required.
- Serve embedded assets in development and production alike. Rebuild Go after
  asset changes; do not add a disk-backed asset mode.
- Keep scheduling and durable session state authoritative on the server.
- Preserve the agreed responsive layout, OS light/dark appearance, keyboard and
  touch controls, and reduced-motion support.
