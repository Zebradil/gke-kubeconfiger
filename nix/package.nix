{
  pkgs,
  self,
}:
let
  fs = pkgs.lib.fileset;
  sourceFiles = fs.unions [
    ../go.mod
    ../go.sum
    ../cmd
    ../main.go
  ];
  baseVersion = "0.8.0";
  commit = self.shortRev or self.dirtyShortRev or "unknown";
  version = "${baseVersion}-${commit}";
in
pkgs.buildGoModule {
  inherit version;
  pname = "gke-kubeconfiger";
  src = fs.toSource {
    root = ./..;
    fileset = sourceFiles;
  };
  vendorHash = "sha256-lzrn0Wx/5EW+4XBzZb9PI98zkh2jqw/hKVKcE2YFbLs=";

  env.CGO_ENABLED = 0;
  doCheck = false;
  ldflags = [
    "-s"
    "-w"
    "-X=main.version=${baseVersion}"
    "-X=main.commit=${commit}"
    "-X=main.date=1970-01-01"
  ];

  meta = {
    changelog = "https://github.com/Zebradil/gke-kubeconfiger/blob/${baseVersion}/CHANGELOG.md";
    description = "Setup kubeconfigs for all accessible GKE clusters";
    homepage = "https://github.com/Zebradil/gke-kubeconfiger";
    license = pkgs.lib.licenses.mit;
    mainProgram = "gker";
  };
}
