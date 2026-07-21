# Container image pins

Built-in profiles use a readable tag plus an immutable registry digest. The tag
documents the intended update line; the digest is the content identity Podman
actually resolves. Pull-mode profiles use `missing` rather than `newer` because
a digest cannot become newer.

The following manifest-list or OCI-index digests were resolved on 2026-07-21:

| Profile/use | Source tag | Immutable digest |
| --- | --- | --- |
| Ubuntu profiles | `docker.io/library/ubuntu:24.04` | `sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90` |
| Debian agent | `docker.io/library/debian:trixie-slim` | `sha256:020c0d20b9880058cbe785a9db107156c3c75c2ac944a6aa7ab59f2add76a7bd` |
| Fedora agent | `registry.fedoraproject.org/fedora-minimal:latest` | `sha256:20abc386771c43ea90126eed66a5dbba7d3ab5fbc3073c3115ad68dd7e390e78` |
| Nix CLI | `docker.io/nixos/nix:latest` | `sha256:377d4887aca98f0dfa12971c1ea6d6a625a435d8b610d4c95a436843da6fbfd1` |
| PowerShell stage | `mcr.microsoft.com/powershell:latest` | `sha256:810c4f1e0c9d23022c3ec18c50a6205ee4b60766f1739d329b2948df1fd7d5b0` |
| .NET SDK stage | `mcr.microsoft.com/dotnet/sdk:10.0-noble` | `sha256:ed034a8bf0b24ded0cbbac07e17825d8e9ebfe21e308191d0f7421eaf5ad4664` |
| Integration smoke test | `docker.io/library/alpine:3.22` | `sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce` |

The PowerShell source index currently publishes Linux amd64 and arm/v7 images,
but no Linux arm64 image. Consequently, the `pwsh-dotnet` built-in is supported
on Linux amd64; the other built-ins use indexes that include Linux amd64 and
arm64. This is an upstream image constraint, not an implicit emulation promise.

For a deliberate refresh, resolve each source tag from its registry, review the
new platform list and upstream release notes, replace the digest, then run
`make check` and `make integration` on the rootless-Podman release host. The
unit suite rejects any future unpinned `FROM` line or built-in pull reference.
