# GKE Kubeconfiger (`gker`)

Setup kubeconfigs for all accessible GKE clusters. It is the same as running `gcloud container clusters get-credentials`
for every cluster in every project but faster.

> [!NOTE]  
> This tool is in beta. Its behavior and interface may change in the future.
> If you have any ideas or suggestions, or if you found a bug, please create an issue or a pull request.

## Usage

> [!IMPORTANT]  
> Make sure Application Default Credentials (ADC) are set up. You can do this by running `gcloud auth application-default login`.

`gker` can work with kubeconfigs in two modes: single file and multiple files. It also has several flags to
control its behavior.

```
gker discovers GKE clusters and updates the KUBECONFIG file to include them.

Usage:
  gker [flags]

Flags:
      --auth-plugin string   Name of the auth plugin to use in kubeconfig (default "gke-gcloud-auth-plugin")
      --batch-size int       Batch size (default 10)
      --config string        config file (default is $HOME/.gker.yaml)
      --dest-dir string      Destination directory to write kubeconfig files.
                             If set, every kubeconfig will be written to a separate file (default ".")
  -h, --help                 help for gker
      --log-level string     Sets logging level: trace, debug, info, warning, error, fatal, panic (default "info")
      --projects strings     Projects to filter by
      --rename               Rename kubeconfig contexts
      --rename-tpl string    Rename template (default "{{ .ProjectID }}/{{ .Location }}/{{ .ClusterName }}")
  -v, --version              version for gker
```

> [!NOTE]  
> Because the `user` part for every connection to a GKE cluster is the same, `gke-kubeconfiger` uses a single `user`
> entry for all contexts that are being added or updated.

### Single kubeconfig file (default)

Without any flags, `gker` will discover all GKE clusters in all GCP projects you have access to and update
or add the corresponding kubeconfig entries.

The kubeconfig file is read from the `KUBECONFIG` environment variable or the default location (`$HOME/.kube/config`).

```shell
# Update the current kubeconfig file in-place
gker
```

### Multiple kubeconfig files

To write every kubeconfig to a separate file, use the `--dest-dir` flag. The kubeconfig files will be named
`<project_id>_<region>_<cluster_name>.yaml` and placed in the specified directory.

```shell
# Write kubeconfigs to the ~/.kube/gke.clusters/ directory for every GKE cluster
gker --dest-dir ~/.kube/gke.clusters/
```

### Filtering

You can filter the projects by using the `--projects` flag. The flag accepts a list of project IDs separated by commas.
Only clusters from the specified projects will be included in the kubeconfig.

```shell
# Include only the specified projects
gker --projects project-1,project-2
```

### Renaming contexts

By default, `gker` names clusters, contexts, and users in the kubeconfig file in the same way as `gcloud`
does, using the following template:

```
gke_{{ .ProjectID }}_{{ .Location }}_{{ .ClusterName }}
```

You can change this behavior by using the `--rename` flag. The `--rename-tpl` flag allows you to specify a custom
template for the context name. The template is parsed using the [text/template](https://pkg.go.dev/text/template)
package with the following fields:

- `.ClusterName` - the cluster name
- `.Location` - the cluster location
- `.ProjectID` - the project ID
- `.Server` - the cluster endpoint with the protocol

Examples of renaming contexts:

```shell
# Default context names (gke_{{ .ProjectID }}_{{ .Location }}_{{ .ClusterName }})
gker
# Rename using the default template ({{ .ProjectID }}/{{ .Location }}/{{ .ClusterName }})
gker --rename
# Rename using a custom template ({{ .ClusterName }})
gker --rename --rename-tpl "{{ .ClusterName }}"
```

### Batch size

By default, `gker` processes clusters in batches of 10. You can change this value using the `--batch-size`

### Auth plugin

By default, `gker` uses the `gke-gcloud-auth-plugin` auth plugin, as `gcloud` does. You can use another auth
plugin by specifying the `--auth-plugin` flag.

```shell
# Use the gke-gcloud-auth-plugin auth plugin
gker
# Use the custom-gke-auth-plugin auth plugin
gker --auth-plugin custom-gke-auth-plugin
```

## Installation

### AUR

```shell
yay -S gke-kubeconfiger-bin
```

### Docker

See the [container registry page](https://github.com/Zebradil/gke-kubeconfiger/pkgs/container/gke-kubeconfiger) for
details.

```shell
docker pull ghcr.io/zebradil/gke-kubeconfiger:latest
```

### DEB, RPM, APK

See the [latest release page](https://github.com/Zebradil/gke-kubeconfiger/releases/latest) for the full list of
packages.

### Nix

The package is not yet available in the Nixpkgs repository. Use nix flake instead.

```shell
nix shell 'github:Zebradil/gke-kubeconfiger/main#gke-kubeconfiger'
```

### Manual

Download the archive for your OS from the [releases page](https://github.com/Zebradil/gke-kubeconfiger/releases).

Or get the source code and build the binary:

```shell
git clone https://github.com/Zebradil/gke-kubeconfiger.git
# OR
curl -sL https://github.com/Zebradil/gke-kubeconfiger/archive/refs/heads/master.tar.gz | tar xz

cd gke-kubeconfiger-master
go build -o gker main.go
```

Now you can run `gker` manually (see [Usage](#usage) section).

## Development

Builds and releases are done with [goreleaser](https://goreleaser.com/).

There are several ways to build the application:

```shell
# Build with go for the current platform
go build -o gker main.go

# Build with GoReleaser for all configured platforms
task go:build
```

Check the [Taskfile.yml](./Taskfile.yml) for more details.

### GeReleaser

:warning: Do not change `.goreleaser.yml` manually, do changes in `.goreleaser.ytt.yml` and run
`task misc:build:goreleaser-config` instead (requires [`ytt`](https://carvel.dev/ytt/) installed).

## License

[MIT](LICENSE)
