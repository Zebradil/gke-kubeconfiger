{
  description = "GKE Kubeconfiger";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };
  outputs = {
    self,
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (system: let
      pkgs = import nixpkgs {inherit system;};
      baseVersion = "0.4.4";
      defaultPackage = pkgs.buildGoModule {
        CGO_ENABLED = "0";
        pname = "gke-kubeconfiger";
        src = ./.;
        vendorHash = "sha256-BJ5sv5zV50xvlfqaeZcmXl/jEZ9zAdrregSTY+3LSYQ=";
        version = "${baseVersion}-${self.shortRev or self.dirtyShortRev or toString self.lastModified or "unknown"}";
        meta = {
          description = "TBD";
        };
      };
    in {
      packages.gke-kubeconfiger = defaultPackage;
      defaultPackage = defaultPackage;

      # Provide an application entry point
      apps.default = flake-utils.lib.mkApp {
        drv = defaultPackage;
        name = "gker";
      };
    });
}
