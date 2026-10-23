{
  description = "ploopy — drive a task document one unit per fresh agent session";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    # nixos-unstable ships OpenCode 2; this pin is the last revision with OpenCode 1.
    nixpkgs-opencode.url = "github:NixOS/nixpkgs/4bcd09ff0157f78d3930877872a2405ee3ee1e9c";
    # Claude Code CLI (rolling: bleeding edge, updated separately).
    claude-code-nix.url = "github:sadjow/claude-code-nix";
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-opencode,
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
        opencode = pkgs.callPackage ./nix/opencode.nix {
          inherit (nixpkgs-opencode.legacyPackages.${pkgs.stdenv.hostPlatform.system}) opencode;
        };
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

      apps = forAllSystems (
        pkgs:
        let
          sandbox = pkgs.writeShellApplication {
            name = "ploopy-sandbox";
            runtimeInputs = [
              pkgs.go
              pkgs.git
            ];
            text = ''
              if [ "$#" -gt 0 ]; then
                dir=$1
                shift
                case "$dir" in
                  /*) ;;
                  *) dir=$PWD/$dir ;;
                esac
                set -- "$dir" "$@"
              fi
              cd ${self}
              exec go run ./dev/sandbox "$@"
            '';
          };
        in
        {
          default = {
            type = "app";
            program = nixpkgs.lib.getExe self.packages.${pkgs.stdenv.hostPlatform.system}.ploopy;
          };
          sandbox = {
            type = "app";
            program = "${sandbox}/bin/ploopy-sandbox";
          };
        }
      );

      overlays.default = final: _prev: {
        ploopy = self.packages.${final.stdenv.hostPlatform.system}.ploopy;
      };

      # The shell can run the loop end to end: all harnesses are here.
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          # Built from the checkout on every call, so the shell's ploopy is
          # the code as it stands; an unchanged tree only costs go's
          # up-to-date check. The build target is never executed, and the
          # binary is swapped in by rename so a running ploopy keeps its own.
          shellHook = ''
            export PLOOPY_SRC=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
          '';
          packages = with pkgs; [
            (writeShellApplication {
              name = "ploopy";
              runtimeInputs = [
                go
                git
                coreutils
                diffutils
              ];
              text = ''
                src=''${PLOOPY_SRC:?enter the dev shell from a ploopy checkout}
                dir=''${XDG_CACHE_HOME:-$HOME/.cache}/ploopy-dev/$(printf %s "$src" | sha256sum | cut -c1-12)
                mkdir -p "$dir"
                rev=$(git -C "$src" describe --always --dirty 2>/dev/null || echo dev)
                go build -C "$src" -o "$dir/build" -ldflags "-X main.version=$rev" ./cmd/ploopy
                if ! cmp -s "$dir/build" "$dir/ploopy"; then
                  cp "$dir/build" "$dir/ploopy.$$"
                  mv -f "$dir/ploopy.$$" "$dir/ploopy"
                fi
                exec "$dir/ploopy" "$@"
              '';
            })
            go
            gopls
            gofumpt
            golangci-lint
            delve
            git
            ripgrep
            jq
            self.packages.${pkgs.stdenv.hostPlatform.system}.opencode
            codex
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
