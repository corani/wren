# Wren

Read and follow [the engineering guidelines](docs/architecture/guidelines.md)
before making changes. They define the project's Go structure, dependencies,
configuration, logging, errors, persistence, testing, and tooling conventions.

For v1 behavior, scope, and approved UI mockups, consult the
[design and implementation plan](docs/plans/0001/README.md).

Prefer table-driven tests with named subtests for related cases. Keep changes
focused and preserve existing vocabulary identities and learning progress.

Use `./build.sh` for standard Go tooling rather than invoking individual tools
directly. Run `./build.sh -h` for actions and `./build.sh -g -t -v -l -b` for
formatting, tests, vetting, linting, and building.
