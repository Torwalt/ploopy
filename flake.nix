{
  description = "ploopy — drive a task document one unit per fresh agent session";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    # Claude Code CLI (rolling: bleeding edge, updated separately).
    claude-code-nix.url = "github:sadjow/claude-code-nix";
  };

  outputs =
    {
      self,
      nixpkgs,
      claude-code-nix,
    }:
    let
      version = "0.1.0";
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: rec {
        default = ploopy;
        ploopy = pkgs.buildGoModule {
          pname = "ploopy";
          inherit version;
          src = self;
          vendorHash = "sha256-Jqt8NHrj4EMt57tq0muEKk9aVpw8nxEKXtLPA6vJ0To=";
          subPackages = [ "cmd/ploopy" ];
          # The loop's tests drive real git repositories in temporary
          # directories, and git refuses to run without a home.
          nativeCheckInputs = [ pkgs.git ];
          preCheck = "export HOME=$TMPDIR";
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${version}"
          ];
          meta = {
            description = "Drive a task document one unit per fresh agent session";
            mainProgram = "ploopy";
            platforms = nixpkgs.lib.platforms.unix;
          };
        };
      });

      apps = forAllSystems (pkgs: {
        default = {
          type = "app";
          program = nixpkgs.lib.getExe self.packages.${pkgs.stdenv.hostPlatform.system}.ploopy;
        };
      });

      overlays.default = final: _prev: {
        ploopy = self.packages.${final.stdenv.hostPlatform.system}.ploopy;
      };

      # The shell can run the loop end to end: both harnesses are here.
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gofumpt
            golangci-lint
            delve
            git
            ripgrep
            jq
            opencode
            claude-code-nix.packages.${pkgs.stdenv.hostPlatform.system}.claude-code
            nixfmt
          ];
        };
      });

      checks = forAllSystems (pkgs: {
        ploopy = self.packages.${pkgs.stdenv.hostPlatform.system}.ploopy;
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);
    };
}
