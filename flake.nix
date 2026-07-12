{
  # Dev shell with everything scripts/build.sh needs. On NixOS the downloaded
  # tailwind standalone can't run (dynamically linked, no glibc loader), so
  # build.sh prefers a tailwindcss from PATH — this shell provides it.
  #
  #   nix develop
  #   ./scripts/build.sh --fast
  description = "Sprout dev shell";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forEachSystem = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      devShells = forEachSystem (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gcc # go test -race (host toolchain)
            zig # static musl cross-builds of the release binaries
            tailwindcss_4
            esbuild
            curl
          ];
        };
      });
    };
}
