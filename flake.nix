{
  description = "Mass-downloader of GKE kubeconfigs";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };
  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        package = import ./nix/package.nix { inherit pkgs self; };
      in
      {
        packages.default = package;
        packages.gke-kubeconfiger = package;
        packages.nix-update = pkgs.nix-update;

        apps.default = flake-utils.lib.mkApp {
          drv = package;
          name = "gker";
        };

        devShells.default = import ./nix/shell.nix { inherit pkgs package; };
      }
    );
}
