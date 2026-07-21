# Usage cheat sheet

This is the short version of the [full usage guide](USAGE_GUIDE.md). Commands
assume `cagent` is on `PATH`, Podman is rootless, and the project directory is
inside an allowed mount root.

## Verify the host

```bash
podman info --format '{{.Host.Security.Rootless}} {{.Host.CgroupVersion}}'
cagent host doctor
cagent version
```

Expected Podman values are `true` and `v2` (or `2`).

## Profiles

```bash
cagent profile list
cagent profile show ubuntu-implementation
cagent profile validate ubuntu-implementation
cagent profile build ubuntu-implementation
cagent profile build ubuntu-implementation --force
```

Built-ins: `ubuntu-implementation`, `debian-agent`, `fedora-agent`,
`pwsh-dotnet`, and `nix-cli`.

## Create an environment

```bash
mkdir -p "$HOME/Development/acme"

cagent env init acme-dev \
  --profile ubuntu-implementation \
  --project "$HOME/Development/acme"

cagent env plan acme-dev
cagent env shell acme-dev
```

Useful creation choices:

```bash
--project-mode read-only
--security sandbox
--resource battery
--rootfs ephemeral
```

## Daily lifecycle

```bash
cagent env list
cagent env show acme-dev
cagent env plan acme-dev
cagent env prepare acme-dev
cagent env start acme-dev
cagent env exec acme-dev -- git status
cagent env exec acme-dev --tty -- /bin/bash
cagent env shell acme-dev
cagent env doctor acme-dev
cagent env diff acme-dev
cagent env stop acme-dev
```

`prepare` creates without starting. `start` leaves the container running.
`shell` and `exec` stop on exit only when that session started the container.

## Drift and deletion

```bash
cagent env diff acme-dev
cagent env recreate acme-dev --confirm acme-dev
cagent env delete acme-dev --confirm acme-dev
cagent env delete acme-dev --confirm acme-dev --delete-home
```

Deletion retains the environment data directory unless `--delete-home` is
specified.

## One-off image run

```bash
cagent run \
  --image docker.io/library/alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce \
  --network none \
  -- /bin/sh
```

With a project mount:

```bash
cagent run \
  --image IMAGE@sha256:DIGEST \
  --project "$PWD" \
  --project-mode read-only \
  -- /bin/sh
```

Raw runs are ephemeral and removed automatically.

## Custom Dockerfile or Containerfile

```bash
# The context must contain .containerignore.
cagent profile detect \
  --containerfile ./container/Containerfile \
  --context ./container \
  --name my-tools

cagent env init my-tools-dev \
  --containerfile ./container/Containerfile \
  --context ./container \
  --project "$PWD" \
  --shell /bin/bash \
  --user agent \
  --home /home/agent
```

See [Other operating systems and images](IMAGES_AND_OTHER_OS.md) for pull,
existing-image, Dockerfile, and Containerfile profile examples.

## Security classes

| Class | Typical use | Important behavior |
| --- | --- | --- |
| `sandbox` | Unknown or diagnostic workload | Read-only root, no network, read-only project, no persistent home, capabilities dropped. |
| `development` | Normal coding | Outbound network by default; no added capabilities. |
| `integration` | Loopback services and integration tests | Permits declared integration networking and ports. |
| `elevated-lab` | Explicit capability experiments | Only class that accepts selected capabilities; never `ALL` or `SYS_ADMIN`. |

## Resource classes

| Class | Intent |
| --- | --- |
| `battery` | Lowest CPU, memory, and build parallelism. |
| `balanced` | Normal default. |
| `performance` | Larger adaptive budget. |
| `custom` | Requires resource values in the environment manifest. |

## Persistence

| Setting | Values |
| --- | --- |
| `spec.home.persistence` | `per-environment`, `ephemeral`, `none` |
| `spec.rootfs.persistence` | `persistent`, `recreate`, `ephemeral`, `read-only` |

`ephemeral` root filesystems are removed by `env stop` and after an owned
shell/exec session. `read-only` applies a read-only container root.

## Exact confirmations

```bash
# Environment exceptions
--allow-dangerous-mount --confirm ENVIRONMENT
--allow-external-port --confirm ENVIRONMENT
--allow-overcommit --confirm ENVIRONMENT

# Raw-run mount exception
--allow-dangerous-mount --confirm raw-run

# Managed cleanup
cagent disk cleanup --managed --confirm managed
```

Confirmations do not override hard denials such as host-root, full-home,
container-socket, `ALL`, or `SYS_ADMIN` access.

## Disk and shutdown checks

```bash
cagent disk report
cagent disk cleanup --plan
cagent disk cleanup --managed --confirm managed
cagent host off-check
cagent host off-check --all-podman
```

`cagent` never performs a global Podman prune or reset.

## JSON and exit codes

```bash
cagent env list --output json
cagent env plan acme-dev --output json
cagent host doctor --output json
cagent disk report --output json
```

| Exit code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Runtime or health failure |
| `2` | Usage error |
| `3` | Policy denial |
| `4` | Required resource not found |

## Files

```text
~/.config/containersagents-v2/defaults.json
~/.config/containersagents-v2/environments/ENVIRONMENT.json
~/.config/containersagents-v2/profiles/PROFILE/profile.json
~/.local/share/containersagents-v2/environments/UUID/
~/.local/state/containersagents-v2/environments/UUID/
~/.cache/containersagents-v2/
```

XDG environment variables change these roots. Use
`cagent host doctor --output json` to see the resolved paths.
