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
    in {
      packages.default = self.outputs.packages.${system}.gke-kubeconfiger;
      packages.gke-kubeconfiger = pkgs.buildGoModule {
        pname = "gke-kubeconfiger";
        version = "0.4.1";
        src = ./.;
        vendorHash = "sha256-o8p/FWGxn85/wcACQP3O49yA6AOOn3l1TkxmADtZpq4=";
      };
    });
}
