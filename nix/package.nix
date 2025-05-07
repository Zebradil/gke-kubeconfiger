{
  pkgs,
  self,
}:
let
  baseVersion = "0.7.30";
  commit = self.shortRev or self.dirtyShortRev or "unknown";
  version = "${baseVersion}-${commit}";
in
pkgs.buildGoModule {
  pname = "gke-kubeconfiger";
  src = self;
  vendorHash = "sha256-uz+g7BiOCCL2JIKeJ4q09pLpZOwsWhW8QK2WpUqvx38=";
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

  postInstall = ''
    mv $out/bin/kubernetes-knot $out/bin/knot
  '';

  meta = {
    changelog = "https://github.com/Zebradil/gke-kubeconfiger/blob/${baseVersion}/CHANGELOG.md";
    description = "Setup kubeconfigs for all accessible GKE clusters";
    homepage = "https://github.com/Zebradil/gke-kubeconfiger";
    license = pkgs.lib.licenses.mit;
    mainProgram = "gker";
  };
}
