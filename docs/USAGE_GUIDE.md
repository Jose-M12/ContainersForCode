# Full usage guide

This guide covers installation, local configuration, profiles, managed
environments, one-off image runs, lifecycle operations, policy, persistence,
networking, secrets, automation, cleanup, and troubleshooting.

For a one-page reference, see the [usage cheat sheet](CHEATSHEET.md). To bring
your own distribution or image, see
[Other operating systems and images](IMAGES_AND_OTHER_OS.md).

## Guide map

- **Set up:** [concepts](#1-what-cagent-manages),
  [prerequisites](#2-supported-host-and-prerequisites),
  [installation](#3-build-and-install-cagent),
  [defaults](#4-configure-local-defaults), and
  [local files](#5-understand-the-local-files).
- **Start working:** [profiles](#6-choose-a-built-in-profile),
  [environment creation](#7-create-the-first-managed-environment),
  [lifecycle](#8-choose-a-lifecycle-operation), and
  [safe edits](#9-edit-an-environment-safely).
- **Choose policy:** [security](#10-security-classes),
  [resources](#11-resource-policy), [persistence](#12-persistence),
  [mounts and concurrency](#13-project-mounts-extra-mounts-and-concurrency),
  and [networking](#14-networking-and-ports).
- **Add integrations:** [secrets](#15-podman-secrets),
  [certificates and proxy](#16-certificates-proxy-and-environment-variables),
  and [raw image runs](#17-run-an-oci-image-without-creating-an-environment).
- **Operate:** [disk cleanup](#18-disk-reports-and-safe-cleanup),
  [automation](#19-automation-and-shell-completion),
  [deletion](#20-delete-environments-and-custom-profiles),
  [troubleshooting](#21-troubleshooting), and [scope](#22-current-scope).

## 1. What cagent manages

ContainersAgents V2 (`cagent`) manages one rootless Podman container per
environment. Four objects make up the workflow:

- A **profile** defines an image, runtime user, home, work directory, shell,
  keepalive command, defaults, and health checks.
- An **environment** selects a profile and adds a project mount, resources,
  security, networking, persistence, secrets, and concurrency policy.
- A **plan** compares the desired profile/environment with stored and actual
  Podman state. Planning is read-only.
- A **session** is a shell, command, or explicitly started container period.

`cagent` invokes the Podman CLI directly. It does not run a daemon, expose the
Podman socket to containers, use Compose, or manage unrelated Podman resources.
It is a container manager, not a virtual-machine manager.

## 2. Supported host and prerequisites

The validated release target is a Linux host with:

- rootless Podman available on `PATH`;
- cgroups v2 for strict CPU and memory controls;
- subordinate UID and GID ranges for the current user;
- enough local memory for the selected profile and resource class;
- Go 1.25.12 or newer when building a release binary;
- `make` and `jq` when running the repository validation targets.

Check Podman before installing `cagent`:

```bash
podman info --format '{{.Host.Security.Rootless}}'
podman info --format '{{.Host.CgroupVersion}}'
```

The expected results are `true` and `v2` (or `2`). Rootless Podman uses the
current user's entries in `/etc/subuid` and `/etc/subgid`; follow the current
[Podman rootless documentation](https://docs.podman.io/en/latest/markdown/podman.1.html)
when those mappings are missing.

Host operating-system boundaries are covered in
[Other operating systems and images](IMAGES_AND_OTHER_OS.md#host-operating-system-support).

## 3. Build and install cagent

Clone and validate the source:

```bash
git clone git@github.com:Jose-M12/ContainersForCode.git
cd ContainersForCode

make check
make coverage
make build
./bin/cagent version
```

Install for the current user:

```bash
mkdir -p "$HOME/.local/bin"
install -m 0755 ./bin/cagent "$HOME/.local/bin/cagent"
export PATH="$HOME/.local/bin:$PATH"
```

Add the `PATH` export to the appropriate shell startup file if it is not
already configured. Then inspect the full host report:

```bash
cagent host doctor
cagent host doctor --output json
```

`host doctor` returns a failure when it finds operational risks, such as
rootful Podman, missing subordinate ID mappings, unavailable secret support, or
unenforceable resource limits. Treat those as host setup failures, not warnings
to ignore.

## 4. Configure local defaults

The default configuration file is:

```text
${XDG_CONFIG_HOME:-$HOME/.config}/containersagents-v2/defaults.json
```

Create it from the repository template:

```bash
config_root="${XDG_CONFIG_HOME:-$HOME/.config}/containersagents-v2"
mkdir -p "$config_root"
cp examples/defaults.json "$config_root/defaults.json"
```

Replace the example paths with directories you actually use:

```json
{
  "apiVersion": "containersagents.dev/v2alpha1",
  "kind": "Defaults",
  "spec": {
    "securityClass": "development",
    "resourceClass": "balanced",
    "allowedMountRoots": [
      "/home/alex/Development",
      "/home/alex/Customers"
    ],
    "strictResources": true,
    "auditMaxMiB": 10,
    "stopOnShellExit": true
  }
}
```

When `allowedMountRoots` is empty, the current home directory is the default
allowed root. The full home directory itself can never be mounted. Mount a
project below it instead. Paths are canonicalized, including symlinks, before
the policy decision.

Defaults apply when a profile or environment does not select a more specific
value. Existing environment manifests keep their explicit settings.

## 5. Understand the local files

The resolved roots follow the XDG base-directory convention:

| Data | Default location |
| --- | --- |
| Defaults | `~/.config/containersagents-v2/defaults.json` |
| Environment manifests | `~/.config/containersagents-v2/environments/` |
| Custom profiles | `~/.config/containersagents-v2/profiles/` |
| Persistent environment data/home | `~/.local/share/containersagents-v2/environments/UUID/` |
| Registration state and audit logs | `~/.local/state/containersagents-v2/environments/UUID/` |
| Capability and materialized build cache | `~/.cache/containersagents-v2/` |

Use this instead of assuming a path:

```bash
cagent host doctor --output json
```

Do not hand-edit applied state or the environment index. Environment manifests,
custom profile manifests, and defaults are intended to be declarative inputs.

## 6. Choose a built-in profile

List, inspect, and validate profiles without starting a container:

```bash
cagent profile list
cagent profile show ubuntu-implementation
cagent profile validate ubuntu-implementation
```

| Built-in | Primary tools and intended use |
| --- | --- |
| `ubuntu-implementation` | Bash, Git, curl, jq, Python, Node.js, npm; general implementation work. |
| `debian-agent` | Similar general agent toolchain on Debian. |
| `fedora-agent` | Fedora-based Bash, Git, Python, Node.js toolchain. |
| `pwsh-dotnet` | PowerShell and .NET SDK. The pinned PowerShell source currently limits this profile to Linux amd64. |
| `nix-cli` | Nix CLI in a pull-based image. |

Build or pull the image ahead of environment creation when desired:

```bash
cagent profile build ubuntu-implementation
cagent profile build nix-cli
```

Build profiles receive content-derived image tags. A later change to the
manifest, Containerfile, or build context produces a different profile hash
and image tag.

## 7. Create the first managed environment

Create an existing host project directory:

```bash
mkdir -p "$HOME/Development/acme-service"
```

Initialize an environment:

```bash
cagent env init acme-dev \
  --profile ubuntu-implementation \
  --project "$HOME/Development/acme-service"
```

Names must start with a lowercase letter, contain only lowercase letters,
digits, and single hyphen-separated segments, and be at most 63 characters.

Useful initialization options are:

| Option | Values and meaning |
| --- | --- |
| `--project-mode` | `read-write` (default) or `read-only` |
| `--security` | `sandbox`, `development`, `integration`, or `elevated-lab` |
| `--resource` | `battery`, `balanced`, `performance`, or `custom` |
| `--rootfs` | `persistent`, `recreate`, `ephemeral`, or `read-only` |

Initialization writes the desired environment manifest and assigns a stable
UUID. It does not build, pull, create, or start the Podman container.

Review the plan:

```bash
cagent env plan acme-dev
cagent env plan acme-dev --output json
```

The plan shows the selected image, resources, mounts, policy exceptions,
warnings, current state, and actions. Planning does not mutate Podman state.

## 8. Choose a lifecycle operation

The normal state flow is:

```text
init -> plan -> prepare -> start -> exec/shell -> stop -> delete
                  |          |
                  |          +-- leaves the container running
                  +-- leaves the container created but stopped
```

### Prepare without starting

```bash
cagent env prepare acme-dev
```

This builds or pulls the profile image, checks referenced secrets, creates an
internal network when required, creates persistent home data when allowed, and
creates the container. It does not start the container.

### Start and leave running

```bash
cagent env start acme-dev
```

The container remains running until an explicit stop, recreate, or delete.

### Open the profile shell

```bash
cagent env shell acme-dev
```

If this shell operation starts the container, it normally stops it on shell
exit. If the container was already running, shell exit does not stop it. The
behavior is controlled by `defaults.spec.stopOnShellExit`.

### Execute a command

```bash
cagent env exec acme-dev -- git status
cagent env exec acme-dev -- python3 --version
cagent env exec acme-dev --tty -- /bin/bash
```

Use `--` to separate `cagent` options from the command. Without a command,
`env exec` opens the profile shell with a TTY.

### Stop

```bash
cagent env stop acme-dev
cagent env stop --all
```

`--all` only targets running containers selected by the V2 management label.
It does not stop unrelated Podman containers.

### Inspect state

```bash
cagent env list
cagent env show acme-dev
cagent env doctor acme-dev
cagent env diff acme-dev
```

- `list` summarizes desired and observed state.
- `show` combines the manifest, resolved profile, stored state, and available
  Podman inspection data.
- `doctor` applies/starts when needed, runs the profile checks, and reports each
  result.
- `diff` reports desired-versus-applied drift without mutation.

## 9. Edit an environment safely

The environment manifest is stored under the configuration root:

```text
~/.config/containersagents-v2/environments/acme-dev.json
```

Edit the manifest, then always plan:

```bash
cagent env plan acme-dev
cagent env diff acme-dev
```

If a container already exists and an immutable desired setting changed, the
environment becomes `DRIFTED`. Review the reasons and recreate explicitly:

```bash
cagent env recreate acme-dev --confirm acme-dev
```

Recreation stops and removes only the UUID-owned container, creates the new
one, and retains the per-environment home. It restarts the replacement only
when the old container was running.

## 10. Security classes

### sandbox

Use for unknown images, reproductions, or read-only diagnostics. It enforces:

- read-only root filesystem;
- all capabilities dropped;
- `no-new-privileges`;
- restricted `/tmp` and `/run` temporary filesystems;
- no persistent home;
- read-only project mount;
- no network.

### development

Use for normal coding. Outbound networking and a writable project are allowed
by the environment policy. Added Linux capabilities remain prohibited.

### integration

Use for declared integration networking or port publication. Rootless,
ownership, mount, and capability restrictions still apply.

### elevated-lab

This is the only class that accepts selected capabilities in
`spec.security.capabilities`. `ALL` and `SYS_ADMIN` remain hard denials. Every
accepted capability is reported and audited as a policy exception.

For the full threat model and hard denials, read the
[security model](SECURITY_MODEL.md).

## 11. Resource policy

The host reserve is removed before calculating the V2 aggregate pool. Resource
classes then choose an adaptive share of that pool and of the host CPUs:

| Class | Memory share/cap | CPU share/cap | Default build jobs |
| --- | --- | --- | --- |
| `battery` | 45%, up to 6 GiB | 35%, up to 4 CPUs | 1 |
| `balanced` | 65%, up to 12 GiB | 55%, up to 8 CPUs | 2 |
| `performance` | 85%, up to 20 GiB | 80%, up to 12 CPUs | 4 |

The selected profile can require minimum memory, CPU, PID, and shared-memory
values. The default combined memory-plus-swap ceiling is memory plus 25%.
Aggregate running V2 memory may not exceed the calculated pool unless an
explicit overcommit is authorized.

For custom resources, set `resourceClass` to `custom` and edit the manifest:

```json
{
  "resourceClass": "custom",
  "resources": {
    "memoryMiB": 4096,
    "swapMiB": 5120,
    "cpus": 2.5,
    "pids": 2048,
    "shmMiB": 512,
    "buildJobs": 2
  }
}
```

`memoryMiB` and `cpus` are mandatory for the custom class. `swapMiB` is the
combined memory-plus-swap ceiling and cannot be lower than `memoryMiB`.

To apply a deliberate aggregate overcommit:

```bash
cagent env plan acme-dev --allow-overcommit
cagent env start acme-dev --allow-overcommit --confirm acme-dev
```

## 12. Persistence

### Home persistence

| Value | Behavior |
| --- | --- |
| `per-environment` | Bind-mounts a private V2 data directory as the profile home when the security class allows it. |
| `ephemeral` | Does not use the persistent host home; the container receives a temporary home. |
| `none` | Does not use the persistent host home; the current runtime also supplies a temporary home path when the profile declares one. |

Sandbox policy always disables the persistent home.

### Root filesystem persistence

| Value | Behavior |
| --- | --- |
| `persistent` | Retains the container and its writable root after stop. |
| `recreate` | Marks recreate-oriented intent; replacement remains explicit with `env recreate`. The current lifecycle retains it after stop. |
| `ephemeral` | Removes the owned container on `env stop` and after a shell/exec/doctor session that started it. |
| `read-only` | Starts with a read-only root plus restricted temporary `/tmp` and `/run`. |

Deleting an environment retains its private data directory by default:

```bash
cagent env delete acme-dev --confirm acme-dev
```

Permanently remove it only when intended:

```bash
cagent env delete acme-dev --confirm acme-dev --delete-home
```

## 13. Project mounts, extra mounts, and concurrency

The project source must exist. Its canonical path must be within an allowed
mount root unless an exception is requested. The mount target must be an
absolute, non-root container path and cannot overlap protected locations.

Additional data mounts can be declared in the environment manifest:

```json
{
  "mounts": [
    {
      "source": "/home/alex/Datasets/sample",
      "target": "/datasets/sample",
      "mode": "read-only"
    }
  ]
}
```

Shared mount propagation is not supported. Sources containing commas,
container-engine sockets, the host root, system paths, and the full host home
are rejected.

Project concurrency modes are:

| Mode | Behavior when the same project is already active |
| --- | --- |
| `exclusive` | Reject another environment. This is the default. |
| `allow` | Permit another environment. Use only when concurrent writers are safe. |
| `read-only-secondary` | Permit this environment only when its project mount is read-only. |
| `prompt` | Rejects in the current non-prompting workflow; an interactive authorization flow is not implemented. |

For a path outside configured roots, planning can show the exception without a
confirmation, but applying requires both values:

```bash
cagent env plan acme-dev --allow-dangerous-mount
cagent env start acme-dev \
  --allow-dangerous-mount \
  --confirm acme-dev
```

Hard-denied sources remain denied.

## 14. Networking and ports

| Mode | Behavior |
| --- | --- |
| `none` | No container network. |
| `outbound` | Normal outbound Podman networking; no published ports. |
| `internal` | A V2-owned Podman internal network is created for the environment. |
| `integration` | Allows declared integration networking and published ports. |

Port publication requires `integration` network mode. A loopback-only example:

```json
{
  "securityClass": "integration",
  "network": {
    "mode": "integration",
    "ports": [
      {
        "hostIP": "127.0.0.1",
        "hostPort": 8080,
        "containerPort": 8080,
        "protocol": "tcp"
      }
    ]
  }
}
```

After changing ports, plan and recreate the container. Non-loopback bindings
also require an explicit exception:

```bash
cagent env recreate acme-dev \
  --allow-external-port \
  --confirm acme-dev
```

The same `--confirm` value authorizes recreation and the declared exception.

## 15. Podman secrets

`cagent` refers to Podman secrets by name; it does not create or store their
values. Create a secret outside `cagent` without placing the value in command
history:

```bash
read -r -s -p 'Secret value: ' secret_value
printf '\n'
printf '%s' "$secret_value" | podman secret create acme-api-token -
unset secret_value
```

Reference it in the environment manifest:

```json
{
  "secrets": [
    {
      "name": "acme-api-token",
      "target": "/run/secrets/acme-api-token",
      "uid": 1000,
      "gid": 1000,
      "mode": 256
    }
  ]
}
```

JSON mode `256` is octal `0400`. Secret targets must be `/run/secrets` or a
path beneath it. Secret-like environment and build-argument names are rejected;
use `spec.secrets` instead.

## 16. Certificates, proxy, and environment variables

Profiles that support certificate injection declare a container CA bundle path.
Add PEM certificates through the environment manifest:

```json
{
  "certificates": [
    {
      "source": "/home/alex/Certificates/customer-root-ca.pem",
      "name": "customer-root"
    }
  ]
}
```

The certificate file must contain a PEM certificate and no private key. Its
source path passes the same mount-root validation. `cagent` extracts the
profile's existing CA bundle, appends the declared certificates, and mounts a
private generated bundle. Profiles without `runtime.caBundle` fail closed.

Non-secret proxy settings and environment values can be declared as follows:

```json
{
  "proxy": {
    "http": "http://proxy.example:8080",
    "https": "http://proxy.example:8080",
    "noProxy": ["localhost", "127.0.0.1", ".internal.example"]
  },
  "environment": {
    "APP_ENV": "development",
    "LOG_LEVEL": "debug"
  }
}
```

Proxy URLs may not contain credentials. Environment-variable names that look
like passwords, tokens, API keys, private keys, secrets, or credentials are
rejected.

## 17. Run an OCI image without creating an environment

A raw run is ephemeral, automatically removed, capability-dropped,
`no-new-privileges`, resource-limited, and unmounted unless a project is
explicitly selected:

```bash
cagent run \
  --image docker.io/library/alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce \
  --network none \
  -- /bin/sh
```

Run against a project:

```bash
cagent run \
  --image IMAGE@sha256:DIGEST \
  --project "$PWD" \
  --project-mode read-only \
  --resource battery \
  -- /bin/sh
```

Raw runs support `none` or `outbound` networking. The project defaults to
read-only. `battery`, `balanced`, and `performance` are directly usable raw-run
resource classes; custom resources belong in a managed environment manifest.

For a project outside configured roots:

```bash
cagent run \
  --image IMAGE@sha256:DIGEST \
  --project /approved/after-review \
  --allow-dangerous-mount \
  --confirm raw-run \
  -- /bin/sh
```

## 18. Disk reports and safe cleanup

Report V2 resources and local data:

```bash
cagent disk report
cagent disk report --output json
```

Show orphaned V2 resources without applying:

```bash
cagent disk cleanup
cagent disk cleanup --plan
```

Apply the reviewed plan:

```bash
cagent disk cleanup --managed --confirm managed
```

Candidates are limited to orphaned V2-labeled stopped containers, owned
networks, unused V2 profile images, and unused materialized profile contexts.
Container and network ownership is rechecked immediately before mutation.
`cagent` never calls a global Podman prune or reset.

Before suspending or shutting down:

```bash
cagent host off-check
cagent host off-check --all-podman
```

The default checks V2 workloads only. `--all-podman` is read-only and reports
all rootless Podman containers visible to the current user.

## 19. Automation and shell completion

Commands that support structured output accept `--output json`:

```bash
cagent profile list --output json
cagent env list --output json
cagent env plan acme-dev --output json
cagent host doctor --output json
cagent disk report --output json
```

Exit codes are stable:

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Runtime, command, or health failure |
| `2` | Invalid usage |
| `3` | Policy denial |
| `4` | Required resource not found |

Generate basic completion:

```bash
cagent completion bash
cagent completion zsh
cagent completion fish
```

For Bash, one simple installation is:

```bash
mkdir -p "$HOME/.local/share/bash-completion/completions"
cagent completion bash > "$HOME/.local/share/bash-completion/completions/cagent"
```

## 20. Delete environments and custom profiles

Delete an environment while retaining its persistent data:

```bash
cagent env delete acme-dev --confirm acme-dev
```

Delete the environment and its private data:

```bash
cagent env delete acme-dev --confirm acme-dev --delete-home
```

Remove an unused custom profile:

```bash
cagent profile remove custom-tools --confirm custom-tools
```

Built-in profiles cannot be removed. A custom profile cannot be removed while
an environment references it. Profile image cleanup is a separate reviewed
`disk cleanup` operation.

## 21. Troubleshooting

### Podman reports rootless=false

Run Podman as the normal user, not through `sudo`. Verify the active Podman
connection and subordinate UID/GID mappings. `cagent` intentionally refuses a
rootful engine.

### Strict resources are unavailable

Confirm the host and the active Podman connection report cgroups v2. A remote
or virtualized Podman connection must expose enforceable resource controls.
Disabling strict resources weakens the release safety model and is not the
recommended fix.

### Mount source is outside allowed roots

Prefer adding the intended parent directory to `defaults.spec.allowedMountRoots`
and re-running the plan. Use `--allow-dangerous-mount` only for a reviewed,
one-time exception. The flag cannot override a hard denial.

### Environment is drifted

```bash
cagent env diff ENVIRONMENT
cagent env plan ENVIRONMENT
cagent env recreate ENVIRONMENT --confirm ENVIRONMENT
```

Do not remove or rename Podman resources manually to hide drift.

### Existing image is unavailable

An `existing` profile never pulls. Load, pull, or build the exact reference
with rootless Podman first, then validate and build the profile again.

### Build context is rejected

Ensure the context:

- contains `.containerignore`;
- is smaller than 2 GiB;
- excludes `.env`, private keys, credential files, and other suspicious paths;
- contains the selected Dockerfile/Containerfile;
- does not resolve to the full host home.

### Project is already in use

Stop the other environment, use a read-only secondary environment with
`read-only-secondary`, or deliberately select `allow` after confirming that
concurrent writers are safe.

### A command works with Podman but not cagent

Direct Podman can request privileges that violate the V2 policy. Inspect the
plan, [security model](SECURITY_MODEL.md), mount roots, selected security class,
and ownership labels. Do not work around the policy by mounting the Podman or
Docker socket.

## 22. Current scope

The current `v2alpha1` release manages one container per environment. It does
not implement multi-container stacks, Compose service dependencies, remote
controller services, a GUI, domain-enforcing egress proxies, automatic
reconciliation, or external build-secret providers. See
[Implementation status](IMPLEMENTATION_STATUS.md) for the authoritative scope.
