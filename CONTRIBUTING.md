# Contributing

Contributions are welcome. Here's how to get started.

## Development Setup

```sh
git clone https://github.com/Reverie-Development-Inc/claude-notify.git
cd claude-notify
make build
make test
```

Requires Go 1.24+.

## Making Changes

1. Fork the repo and create a feature branch from `main`.
2. Write tests for new functionality.
3. Run `make test` and ensure all tests pass with `-race`.
4. Run `go vet ./...` to check for issues.
5. Keep commits focused and write clear commit messages.
6. Open a pull request against `main`.

## Code Style

- Standard Go formatting (`gofmt`).
- Keep packages focused: one responsibility per package.
- Error messages should be lowercase, no trailing punctuation.
- Use `fmt.Errorf` with `%w` for error wrapping.
- Platform-specific code uses build tags (e.g.,
  `//go:build !windows`).

## Testing

- Unit tests live alongside the code (`*_test.go`).
- Integration tests are in `tests/`.
- All tests run with `-race` in CI.
- Mock external dependencies (Discord API, AWS SSM) in tests.

## What Makes a Good PR

- Solves one problem or adds one feature.
- Includes tests for new behavior.
- Updates README.md if user-facing behavior changes.
- Doesn't break existing tests.

## Security

If you find a security vulnerability, please report it
privately via GitHub Security Advisories rather than opening
a public issue. See [SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions will be
licensed under the MIT License.
