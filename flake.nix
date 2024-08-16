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
      baseVersion = "0.4.6";
      commit =
        if (self ? shortRev)
        then self.shortRev
        else if (self ? dirtyShortRev)
        then self.dirtyShortRev
        else "unknown";
      version = "${baseVersion}-${commit}";
      defaultPackage = pkgs.buildGoModule {
        CGO_ENABLED = "0";
        pname = "gke-kubeconfiger";
        src = ./.;
        vendorHash = "sha256-9kZ8oNhDWJNUOgQ24eAJnT9kT6Z60WSeOomWO0T9Em0=";
        version = version;

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
          license = nixpkgs.lib.licenses.mit;
          mainProgram = "gker";
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

      devShells.default = pkgs.mkShell {
        packages = with pkgs; [
          # TODO: add semantic-release and plugins
          gnused
          go
          go-task
          gofumpt
          goimports-reviser
          golangci-lint
          goreleaser
          gosec
          nix-update
          ytt
        ];
      };
    });
}
