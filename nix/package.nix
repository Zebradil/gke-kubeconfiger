{
  pkgs,
  self,
}:
let
  baseVersion = "0.7.41";
  commit = self.shortRev or self.dirtyShortRev or "unknown";
  version = "${baseVersion}-${commit}";
in
pkgs.buildGoModule {
  pname = "gke-kubeconfiger";
  src = self;
  vendorHash = "sha256-FIRo3fiRF0BelLdHf/5ohVyGFFPqhmuCRF+pRAX0cFw=";
  version = version;

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
