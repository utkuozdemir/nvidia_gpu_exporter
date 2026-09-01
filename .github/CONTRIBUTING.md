# Contributing

Contributing is simple:

1. Be nice :) The [code of conduct](CODE_OF_CONDUCT.md) applies.
2. If you are not sure about something (e.g., whether it is a bug, how to solve it, whether a feature makes sense), open an issue first so we can discuss it. It may save your time.
3. Fork the repo, make your changes, open a pull request.
4. Add tests for what you change. New functionality comes with tests in the automated suite, and a bug fix comes with a test that would have caught it.
5. Sign off your commits with `git commit -s`. This is the [Developer Certificate of Origin](https://developercertificate.org/): you state that you have the right to contribute the code.
6. Make sure the CI build is green. Address the review comments if there are any.

That's it.
One exception: security problems do not go to the issue tracker, see [SECURITY.md](SECURITY.md).

## Setting up for development

You need Go (the version pinned in `go.mod`).
For the linters, you also need Docker and [Task](https://taskfile.dev).

```bash
git clone https://github.com/utkuozdemir/nvidia_gpu_exporter.git
cd nvidia_gpu_exporter
go build ./...
go test ./...
```

`task test` runs the same tests.
`task fmt` formats the code and the scripts.
`task lint` runs every linter in containers, at the versions CI uses.
This way, the workstation and CI run the same tool versions.

You do not need a GPU.
The test suite replays recorded `nvidia-smi` outputs, and the exporter can run against a fake `nvidia-smi` or in demo mode.
See [AGENTS.md](../AGENTS.md) for the map of the code base and how testing works.

## Code style

- Go code is formatted with `gofmt` and checked by `golangci-lint` with all
  linters enabled. Each disabled linter has a written reason in
  `.golangci.yml`.
- Shell scripts go through `shfmt` and `shellcheck`. Markdown goes through
  `markdownlint`.
- `task lint` runs all of it, and the CI build fails on any finding.

Commit messages follow the conventional commit format,
`type(scope): description`, with a body that explains what changed and why.
The changelog is generated from them.

## Contributing a GPU capture

The exporter parses `nvidia-smi` output, which differs across GPU models, driver versions and operating systems.
A capture from a setup the test corpus has not seen yet is a welcome contribution, and it takes one command.
See [internal/captures/README.md](../internal/captures/README.md).

## Creating releases

Releases are cut by the maintainer:

```bash
task release
```

It needs [task](https://github.com/go-task/task) and Docker, [svu](https://github.com/caarlos0/svu) runs containerized.
The version is derived from the commit history.
The tag is signed, and the release workflow refuses to build from a tag that GitHub does not show as verified.
