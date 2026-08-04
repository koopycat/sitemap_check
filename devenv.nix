{ pkgs, ... }:
# sitemap_check - Go dev shell for the sitemap URL checker CLI.
{
  languages.go.enable = true;

  packages = [
    pkgs.gopls
    pkgs.gotools # goimports, staticcheck, ...
    pkgs.golangci-lint
  ];

  enterShell = ''
    echo "sitemap_check dev shell"
    go version
    echo "Build: go build ./..."
    echo "Test:  go test ./..."
    echo "Lint:  golangci-lint run ./..."
    echo "Run:   go run . https://example.com/sitemap.xml"
  '';
}
