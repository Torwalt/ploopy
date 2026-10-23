{ opencode, fetchFromGitHub }:
opencode.overrideAttrs (
  final: prev: {
    version = "1.19.4";

    src = fetchFromGitHub {
      owner = "anomalyco";
      repo = "opencode";
      tag = "v${final.version}";
      hash = "sha256-anAv4xsGIUHY2RTWF08nXSQVyE9ny/mbUWzfUwfpMwM=";
    };

    passthru = prev.passthru // {
      node_modules = prev.passthru.node_modules.overrideAttrs {
        outputHash = "sha256-3t1n0TQpcRmSOg9IkGud2t4uL/JDsfnRIIF7qb5zXgg=";
      };
    };
  }
)
