# ContainersAgents V2

ContainersAgents V2 is a declarative, rootless-Podman environment manager for customer implementation work, troubleshooting, integration testing, and AI-agent development. It is a new project with no migration or lifecycle relationship to ContainersAgents V1.

The main objects are:

- **Profile** — reusable image and runtime contract.
- **Environment** — one project/customer instance with its own UUID and home.
- **Policy** — security, resources, network, persistence, and concurrency.
- **Session** — one shell, command, or detached running period.

V2 is a single Go binary. It invokes the Podman CLI with argument arrays, needs no controller daemon or Podman socket, publishes no ports by default, and never runs a global Podman prune/reset command.

## Status

This repository implements the Linux-first `v2alpha1` single-container MVP. The controller, schemas, five built-in profiles, lifecycle, security policy, adaptive resource budgets, drift detection, audit logging, raw image runner, safe disk cleanup, tests, and operational documentation are present.

Podman-backed integration tests require a rootless Podman host and are opt-in. Multi-container stacks, remote hosts, feature composition, domain-aware egress enforcement, and a GUI remain intentionally out of scope.

## Documentation

| Start here | Purpose |
| --- | --- |
| [Documentation index](docs/README.md) | Map of all user, design, security, and release documentation. |
| [Full usage guide](docs/USAGE_GUIDE.md) | Installation, configuration, profiles, environments, lifecycle, policy, persistence, networking, secrets, cleanup, automation, and troubleshooting. |
| [Usage cheat sheet](docs/CHEATSHEET.md) | Short daily command reference. |
| [Other operating systems and images](docs/IMAGES_AND_OTHER_OS.md) | Host OS boundaries and how to use OCI/Docker images, local Podman images, archives, Dockerfiles, and Containerfiles. |
| [CLI reference](docs/CLI.md) | Command and option reference. |

## Build

Requirements:

- Go 1.25.12 or newer for release builds (the module retains Go 1.23 language compatibility);
- rootless Podman for runtime commands;
- cgroups v2 for strict resource enforcement.

```bash
make check
make coverage
make build
./bin/cagent version
```

No third-party Go modules are required.

For host preparation, user-local installation, defaults, and verification, use
the [full usage guide](docs/USAGE_GUIDE.md#2-supported-host-and-prerequisites).

CI repeats these checks with the race detector and cross-builds, then runs the
full managed lifecycle on an Ubuntu rootless-Podman, cgroups-v2 runner. The
integration target remains opt-in locally and does not install or configure
Podman.

## First workflow

```bash
cagent host doctor
cagent profile list
cagent env init eve-acme \
  --profile ubuntu-implementation \
  --project "$HOME/Customers/Acme/implementation"
cagent env plan eve-acme
cagent env shell eve-acme
```

The default shell session stops the container on exit only when that session started it. A container started with `cagent env start` remains running until explicitly stopped.

## Custom Containerfile

The build context must be explicit and must contain `.containerignore`:

```bash
cagent env init customer-lab \
  --containerfile ./customer-lab/Containerfile \
  --context ./customer-lab \
  --project "$HOME/Customers/CustomerA/implementation" \
  --shell /bin/bash \
  --user agent \
  --home /home/agent
```

This creates a local profile manifest whose context and Containerfile paths are absolute. It does not copy the build context or customer files into V2 configuration.

See [Other operating systems and images](docs/IMAGES_AND_OTHER_OS.md) for full
pull-profile, existing-image, archive, Dockerfile, Containerfile, registry, and
image-update workflows.

## Raw OCI image

```bash
cagent run --image mcr.microsoft.com/dotnet/sdk:10.0-noble@sha256:ed034a8bf0b24ded0cbbac07e17825d8e9ebfe21e308191d0f7421eaf5ad4664 -- dotnet --info
cagent run --image docker.io/nixos/nix:latest@sha256:377d4887aca98f0dfa12971c1ea6d6a625a435d8b610d4c95a436843da6fbfd1 -- nix --version
```

Raw runs are ephemeral, auto-removed, resource-limited, capability-dropped, and receive no project mount unless explicitly requested.

## Safety model

Hard MVP denials include privileged mode, host namespaces/network, host root/home/system mounts, container-engine sockets, unvalidated devices, global cleanup, and secret-like values in manifest environment variables or build arguments. Non-loopback port publication, external mount roots, aggregate-memory overcommit, recreation, deletion, and managed cleanup require explicit flags and confirmation tokens.

See the [documentation index](docs/README.md) for every guide, schema, and
design document.
