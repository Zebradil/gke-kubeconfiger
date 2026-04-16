# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

The project uses [Task](https://taskfile.dev/) as the command runner. `task` with no args lists available tasks.

```shell
task go:fmt          # goimports-reviser + gofumpt -w .
task go:lint         # runs golangci-lint + go vet + gosec in parallel
task go:lint:golangci
task go:lint:vet
task go:lint:sec
task go:build        # goreleaser build --snapshot --single-target -> bin/gker
task go:run -- <args>  # rebuilds and runs bin/gker

go build -o gker main.go   # quick local build without goreleaser
go test -v ./...           # CI runs this; there are currently no tests in the repo
```

A Nix flake is provided: `nix develop` enters a dev shell (see `nix/shell.nix`); `nix build` produces `gker` via `nix/package.nix`.

## Architecture

Entry point: `main.go` injects `version`/`commit`/`date` (overridden by GoReleaser/Nix ldflags) and hands off to `cmd.NewRootCmd`. Everything else lives in `cmd/main.go` — a single-package CLI.

The `run` function in `cmd/main.go` is a **streaming pipeline** built from goroutines and channels, with a single `semaphore chan struct{}` of size `--concurrency` gating all outbound Google API calls:

1. `getProjects()` — lists all active projects via Cloud Resource Manager (skipped if `--projects` is provided).
2. `filterProjects()` — for each project, checks via Service Usage API whether `container.googleapis.com` is `ENABLED`; emits project IDs on `filteredProjects`.
3. `getCredentials()` — for each enabled project, lists clusters via the Container API and emits one `credentialsData` per cluster on `credentials`.
4. Terminal stage depends on mode:
   - **Single-file mode** (default): `inflateKubeconfig` merges entries into a kubeconfig read upfront from `$KUBECONFIG` (or `$HOME/.kube/config`), then `writeKubeconfigToFile` overwrites that path.
   - **Split mode** (`--dest-dir` set): `writeCredentialsToFile` writes one `<project>_<location>_<cluster>.yaml` per cluster into `DestDir`.

`replaceOrAppend` upserts entries in the `clusters` / `contexts` / `users` lists of a kubeconfig represented as `map[string]interface{}` (parsed via `gopkg.in/yaml.v3`). All contexts share a single `users` entry named by the `userName` constant (`"gke-kubeconfiger"`) that invokes the exec auth plugin — changing this name would invalidate existing kubeconfigs written by prior runs.

Known limitation documented in `cmd/main.go`: the single-file mode reads the kubeconfig upfront and overwrites it after all API calls complete; there is no file locking, so concurrent writers can cause data loss.

### Configuration layering

Flags are declared on the Cobra root command and bound to Viper via `viper.BindPFlags`. `initConfig` additionally enables `viper.AutomaticEnv()` with prefix `GKEKC` and replacer `. -> _`, `- -> _`, so every flag has an env-var equivalent (e.g. `--auth-plugin` → `GKEKC_AUTH_PLUGIN`). A YAML config file is loaded from `--config` or `$HOME/.gker.yaml`. The `run` function reads everything back out of Viper — flags are not the source of truth.

The `--rename` flag switches the context-name template: when unset, it is forced to `gke_{{ .ProjectID }}_{{ .Location }}_{{ .ClusterName }}` (matching `gcloud`) regardless of `--rename-tpl`. Template fields are `ProjectID`, `Location`, `ClusterName`, `Server`.

## Release / packaging gotchas

- **Do not edit `.goreleaser.yml` by hand.** It is generated from `.goreleaser.ytt.yml` via `task misc:build:goreleaser-config` (requires [`ytt`](https://carvel.dev/ytt/)). Hand edits will be overwritten.
- `nix/package.nix` pins `baseVersion` and `vendorHash`. After changing `go.mod`/`go.sum`, update `vendorHash` (`.github/scripts/update-flake-version` handles this in CI on PRs).
- Releases are driven by `semantic-release` (`.releaserc.yml`) on pushes to `main`; the `chore(release): …` commits are produced by CI — do not craft them manually.
- `Dockerfile` is `FROM scratch` and expects the binary to already be built at `$TARGETPLATFORM/gker`; it is intended to be consumed by GoReleaser's `dockers:` step, not `docker build .` directly.

## Conventions

- Fatal errors use `log.Fatalf` / `log.WithError(...).Fatal(...)` from `sirupsen/logrus`; the code generally aborts rather than returning errors up the call stack.
- `#nosec G304` annotations on `os.OpenFile` / `os.ReadFile` are intentional — gosec is part of `task go:lint` and will fail on user-supplied kubeconfig paths without them.
- Commits follow Conventional Commits (`feat:`, `fix:`, `chore(deps):`, etc.) — this is what drives semantic-release.
