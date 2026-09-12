{
  description = "lifier — Amp runners in composable sandboxes: containers, Virtualization.framework, QEMU/KVM";

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
    flake-utils.lib.eachDefaultSystem (
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
            pkgs.nixfmt-rfc-style
          ];
          text = ''
            if [ "$#" -gt 0 ]; then
              exec nixfmt "$@"
            fi

            exec fd --extension nix --type file --exec-batch nixfmt
          '';
        };

        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.gotools
            pkgs.go-tools
            pkgs.gofumpt
            pkgs.go-task
            pkgs.shfmt
            pkgs.lima
          ];
          shellHook = ''
            export GOTOOLCHAIN=local
          '';
        };
      }
    );
}
