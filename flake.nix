{
  description = "lifier — Amp runners in composable sandboxes: Docker, Lima, Kubernetes Pods, and KubeVirt";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      nixpkgs,
      flake-utils,
      ...
    }:
    # Not eachDefaultSystem: nixpkgs unstable has dropped x86_64-darwin, so
    # evaluating it (as `nix flake check --all-systems` does) throws.
    flake-utils.lib.eachSystem
      [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ]
      (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        {
          packages.default = pkgs.buildGoModule {
            pname = "lifier";
            version = "0.1.0";
            src = ./.;
            # vendorHash is recomputed when the module graph changes; run
            # `nix build` and copy the hash it reports here on dependency bumps.
            vendorHash = "sha256-pjhPGqSnizYfAmjJukyUVj2O13kybI6BjMH/J7G/900=";
            subPackages = [ "cmd/lifier" ];
            ldflags = [
              "-s"
              "-w"
              "-X"
              "main.version=0.1.0"
            ];
            meta = {
              description = "Run Amp runners in disposable container and virtual-machine sandboxes";
              mainProgram = "lifier";
            };
          };

          # `nix fmt` with no arguments would otherwise be handed every path in the
          # tree, including the Go and Markdown files nixfmt cannot parse.
          formatter = pkgs.writeShellApplication {
            name = "lifier-nixfmt";
            runtimeInputs = [
              pkgs.fd
              pkgs.nixfmt
            ];
            text = ''
              if [ "$#" -gt 0 ] && [ "$1" != "--check" ]; then
                exec nixfmt "$@"
              fi

              exec fd --extension nix --type file --exec-batch nixfmt "$@"
            '';
          };

          checks.shell =
            pkgs.runCommand "lifier-shellcheck"
              {
                src = ./.;
                nativeBuildInputs = [ pkgs.shellcheck ];
              }
              ''
                shellcheck "$src"/hack/*.sh
                touch "$out"
              '';

          devShells.default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              pkgs.gotools
              pkgs.go-tools
              pkgs.gofumpt
              pkgs.go-task
              pkgs.shfmt
              pkgs.shellcheck
              pkgs.lima
              pkgs.kubectl
              pkgs.kubevirt
              pkgs.docker-client
              pkgs.python3
              pkgs.openssh
              pkgs.nodejs
              pkgs.typescript
            ];
            shellHook = ''
              export GOTOOLCHAIN=local
            '';
          };
        }
      );
}
