# syntax=docker/dockerfile:1.26
#
# The development and CI toolchain, one stage per check.
#
# Every tool this repository lints, formats or tests with lives here, pinned, so
# a fresh clone needs only docker and task on the host and CI runs the exact
# same versions instead of installing its own. The Taskfile drives the stages
# through `docker buildx build --target`.
#
# Two properties matter and are easy to lose:
#
#   - Cache shape. Tools arrive as released binaries rather than compiled from
#     source, so no check waits on a toolchain build. The module graph is
#     downloaded in a stage that does not sit downstream of the tools, so
#     bumping a linter does not re-download it. The Go build and module caches
#     are shared BuildKit caches keyed per repository.
#   - Linux. Large parts of internal/nvmlnative are behind a `linux && cgo`
#     build tag, so a host toolchain on macOS excludes them from the build
#     entirely and lints nothing at all. Running the checks here is the only way
#     the code CI sees is the code checked locally.
#
# GO_VERSION has no default on purpose: the Taskfile passes the version go.mod
# declares, so the toolchain cannot drift from the module.

ARG GO_VERSION
# renovate: depName=golangci/golangci-lint datasource=github-releases
ARG GOLANGCI_LINT_VERSION=2.13.1
# renovate: depName=mvdan/sh datasource=github-releases
ARG SHFMT_VERSION=3.13.1
# renovate: depName=grafana/dashboard-linter datasource=github-releases
ARG DASHBOARD_LINTER_VERSION=0.2.0
# renovate: depName=markdownlint-cli datasource=npm
ARG MARKDOWNLINT_VERSION=0.49.1
# renovate: depName=node datasource=docker
ARG NODE_VERSION=24.19.0
# renovate: depName=koalaman/shellcheck datasource=docker
ARG SHELLCHECK_VERSION=v0.11.0
# renovate: depName=goreleaser/goreleaser datasource=docker
ARG GORELEASER_VERSION=v2.18.0

FROM koalaman/shellcheck:${SHELLCHECK_VERSION} AS shellcheck-bin
FROM goreleaser/goreleaser:${GORELEASER_VERSION} AS goreleaser-bin

# released binaries, fetched rather than compiled, so no check waits on a
# toolchain build. The downloads are a linear chain: bumping an earlier tool
# re-fetches the later ones, which is a few seconds and not worth a stage per
# tool.
FROM alpine:3 AS tools
RUN apk add --no-cache curl tar git
ARG TARGETARCH
ARG GOLANGCI_LINT_VERSION
RUN curl -sSfL "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${TARGETARCH}.tar.gz" \
    | tar -xz -C /usr/local/bin --strip-components=1 "golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${TARGETARCH}/golangci-lint"
ARG SHFMT_VERSION
RUN curl -sSfL -o /usr/local/bin/shfmt "https://github.com/mvdan/sh/releases/download/v${SHFMT_VERSION}/shfmt_v${SHFMT_VERSION}_linux_${TARGETARCH}" \
    && chmod +x /usr/local/bin/shfmt
ARG DASHBOARD_LINTER_VERSION
RUN curl -sSfL "https://github.com/grafana/dashboard-linter/releases/download/v${DASHBOARD_LINTER_VERSION}/dashboard-linter_${DASHBOARD_LINTER_VERSION}_linux_${TARGETARCH}.tar.gz" \
    | tar -xz -C /usr/local/bin dashboard-linter
COPY --from=shellcheck-bin /bin/shellcheck /usr/local/bin/shellcheck
COPY --from=goreleaser-bin /usr/bin/goreleaser /usr/local/bin/goreleaser

# the Go toolchain and the module graph. Deliberately NOT downstream of `tools`:
# a linter bump must not re-download the modules.
FROM golang:${GO_VERSION} AS deps
ENV CGO_ENABLED=1
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build,id=nvidia_gpu_exporter/go-build \
    --mount=type=cache,target=/go/pkg,id=nvidia_gpu_exporter/go-pkg \
    go mod download

# only the Go tree: a docs, dashboard or packaging edit must not rerun the Go
# checks
FROM deps AS base
COPY cmd ./cmd
COPY internal ./internal

FROM base AS lint-golangci-lint
COPY .golangci.yml ./
COPY --from=tools /usr/local/bin/golangci-lint /usr/local/bin/
RUN --mount=type=cache,target=/root/.cache/go-build,id=nvidia_gpu_exporter/go-build \
    --mount=type=cache,target=/go/pkg,id=nvidia_gpu_exporter/go-pkg \
    --mount=type=cache,target=/root/.cache/golangci-lint,id=nvidia_gpu_exporter/golangci-lint \
    golangci-lint run --timeout=5m ./...

FROM base AS lint-go-mod-tidy
RUN --mount=type=cache,target=/root/.cache/go-build,id=nvidia_gpu_exporter/go-build \
    --mount=type=cache,target=/go/pkg,id=nvidia_gpu_exporter/go-pkg \
    go mod tidy --diff

FROM base AS unit-tests
RUN --mount=type=cache,target=/root/.cache/go-build,id=nvidia_gpu_exporter/go-build \
    --mount=type=cache,target=/go/pkg,id=nvidia_gpu_exporter/go-pkg \
    go test -race -coverpkg=./... -coverprofile=/tmp/coverage.out -covermode=atomic -timeout 20m ./...

# the GPU-tagged parity test never runs without hardware, but it must keep
# compiling so it cannot rot between the rare sessions where a GPU exists
FROM base AS lint-gpu-tagged-test
RUN --mount=type=cache,target=/root/.cache/go-build,id=nvidia_gpu_exporter/go-build \
    --mount=type=cache,target=/go/pkg,id=nvidia_gpu_exporter/go-pkg \
    go test -run '^$' -tags gpu ./internal/integration/

FROM node:${NODE_VERSION}-alpine AS lint-markdown
ARG MARKDOWNLINT_VERSION
RUN npm install --global markdownlint-cli@${MARKDOWNLINT_VERSION}
WORKDIR /src
COPY . .
# --dot: without it the tracked markdown under .github/ is skipped entirely
RUN markdownlint --dot .

FROM tools AS lint-shell
WORKDIR /src
COPY . .
RUN find . -name '*.sh' -print0 | xargs -0 shellcheck
RUN find . -name '*.sh' -print0 | xargs -0 shfmt -d -i 2 -ci -sr

# only the dashboards, so unrelated edits leave this cached
FROM tools AS lint-dashboard
WORKDIR /src
COPY docs/grafana ./docs/grafana
RUN dashboard-linter lint --strict docs/grafana/dashboard.json
RUN dashboard-linter lint --strict docs/grafana/dashboard-overview.json

# goreleaser refuses to run outside a repository with a remote, but it only
# reads the config. A synthetic repository satisfies it, which keeps .git out of
# the build context entirely: .git changes on every commit and would otherwise
# invalidate every stage that copies the tree.
FROM tools AS lint-goreleaser
WORKDIR /src
COPY .goreleaser.yml ./
RUN git init -q . \
    && git remote add origin https://github.com/utkuozdemir/nvidia_gpu_exporter.git \
    && goreleaser check

# the formatters run against a bind-mounted working tree rather than a stage
# export, so this stage carries the tools and the Go toolchain but no sources
FROM deps AS fmt
COPY --from=tools /usr/local/bin/golangci-lint /usr/local/bin/shfmt /usr/local/bin/
