# gke-kubeconfiger

Setup kubeconfigs for all accessible GKE clusters. It is the same as running
`gcloud container clusters get-credentials` for every cluster in every project
but faster.

> [!NOTE] This tool is in experimental stage. Its behavior and interface _will_
> change.

> [!IMPORTANT] Any help is appreciated. If you have any ideas, suggestions, or
> issues, please open an issue or a pull request.

## Usage

```
Usage of ./bin/gke-kubeconfiger:
  -batch-size int
        Batch size (default 10)
  -rename
        Rename kubeconfig contexts
  -rename-tpl string
        Rename template (default "{{ .ProjectID }}/{{ .Location }}/{{ .ClusterName }}")
```

Without any flags, `gke-kubeconfiger` will create a kubeconfig file for every
cluster in every project you have access to. The kubeconfig files will be named
`<project_id>_<region>_<cluster_name>.yaml` and placed in the current directory.

Default context names are in the form of
`gke_<project_id>_<region>_<cluster_name>`. You can change this behavior by
using the `-rename` flag. The `-rename-tpl` flag allows you to specify a custom
template for the context name. The template is parsed using the
[text/template](https://pkg.go.dev/text/template) package with the following
fields:

- `.ProjectID` - the project ID
- `.Location` - the cluster location
- `.ClusterName` - the cluster name

Examples of renaming contexts:

```shell
# Default context names (gke_<project_id>_<region>_<cluster_name>)
gke-kubeconfiger
# Rename using the default template (<project_id>/<region>/<cluster_name>)
gke-kubeconfiger -rename
# Rename using a custom template (<cluster_name>)
gke-kubeconfiger -rename -rename-tpl "{{ .ClusterName }}"
```

## Installation

### AUR

```shell
yay -S gke-kubeconfiger-bin
```

### Docker

See the
[container registry page](https://github.com/Zebradil/gke-kubeconfiger/pkgs/container/gke-kubeconfiger)
for details.

```shell
docker pull ghcr.io/zebradil/gke-kubeconfiger:latest
```

### DEB, RPM, APK

See the
[latest release page](https://github.com/Zebradil/gke-kubeconfiger/releases/latest)
for the full list of packages.

### Manual

Download the archive for your OS from the
[releases page](https://github.com/Zebradil/gke-kubeconfiger/releases).

Or get the source code and build the binary:

```shell
git clone https://github.com/Zebradil/gke-kubeconfiger.git
# OR
curl -sL https://github.com/Zebradil/gke-kubeconfiger/archive/refs/heads/master.tar.gz | tar xz

cd gke-kubeconfiger-master
go build -o gke-kubeconfiger main.go
```

Now you can run `gke-kubeconfiger` manually (see [Usage](#usage) section).

## Development

Builds and releases are done with [goreleaser](https://goreleaser.com/).

There are several ways to build the application:

```shell
# Build with go for the current platform
go build -o gke-kubeconfiger main.go

# Build with GoReleaser for all configured platforms
task go:build
```

Check the [Taskfile.yml](./Taskfile.yml) for more details.

### GeReleaser

:warning: Do not change `.goreleaser.yml` manually, do changes in
`.goreleaser.ytt.yml` and run `task misc:build:goreleaser-config` instead
(requires [`ytt`](https://carvel.dev/ytt/) installed).

## License

[MIT](LICENSE)
