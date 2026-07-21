# Other operating systems, OCI images, Dockerfiles, and Containerfiles

This guide explains how to use `cagent` on supported hosts and how to bring a
different Linux distribution, a remote OCI/Docker image, an image already in
Podman, an image archive, or a Dockerfile/Containerfile.

## Guide map

- [Host operating-system support](#host-operating-system-support)
- [Adding another Linux distribution](#add-another-linux-distribution)
- [Choosing the correct image path](#choose-the-correct-image-path)
- [Reusable pull profiles](#create-a-reusable-pull-profile)
- [Existing Podman images](#use-an-image-already-in-podman)
- [OCI and Docker archives](#load-an-oci-or-docker-image-archive)
- [Dockerfiles and Containerfiles](#build-from-a-dockerfile-or-containerfile)
- [Manual advanced build profiles](#create-an-advanced-manual-build-profile)
- [Runtime compatibility checklist](#runtime-contract-checklist)
- [Private registries](#private-registries)
- [Image updates](#image-update-procedure)
- [Common failures](#common-failures)

## Host operating-system support

### Linux

Linux is the supported and CI-validated host. The distribution can vary as long
as it provides:

- rootless Podman on `PATH`;
- cgroups v2;
- subordinate UID/GID mappings;
- a local filesystem suitable for rootless container storage;
- Go 1.25.12 or newer when building `cagent`.

Verify the effective runtime instead of relying on the distribution name:

```bash
podman info --format '{{.Host.Security.Rootless}} {{.Host.CgroupVersion}}'
cagent host doctor
```

Podman's current [rootless documentation](https://docs.podman.io/en/latest/markdown/podman.1.html)
explains subordinate IDs, user namespaces, and rootless storage constraints.

### macOS

The CLI can be compiled for macOS and host memory discovery is implemented, but
the project is Linux-first and the rootless-Podman lifecycle is not currently a
release-tested macOS target. Podman on macOS runs containers inside a Podman
Machine Linux VM. Treat this as experimental:

1. Install Podman or Podman Desktop using the official
   [Podman Desktop installation guide](https://podman-desktop.io/docs/installation).
2. Create and start a Podman Machine.
3. Select a rootless connection.
4. Build `cagent` with `GOOS=darwin` or run it from source.
5. Run `cagent host doctor` and stop if rootless operation or strict resources
   are not reported.
6. Start with a project under the user home and the `sandbox` security class.

Do not call an experimental setup production-ready until the repository's full
`make integration` target passes on that machine.

### Windows

Native Windows execution is not a supported runtime target in this release.
The binary can cross-compile, but automatic host memory discovery and Linux
container path behavior are not complete on native Windows.

Use a Linux WSL2 distribution and run both rootless Podman and `cagent` inside
that Linux environment. Podman Desktop itself uses a Linux VM on Windows; see
the official [Windows installation guide](https://podman-desktop.io/docs/installation/windows-install).
Regardless of the installation method, `cagent` requires the active Podman
connection to report rootless operation.

### FreeBSD and other hosts

They are not supported by the current automatic host-resource discovery. Run
`cagent` on a supported Linux host or Linux virtual machine instead.

## Container operating systems are not host operating systems

A container image provides a Linux user space, not a complete guest operating
system. You can use Alpine, Debian, Ubuntu, Fedora, Rocky Linux, Nix, or another
Linux distribution image when it:

- supports the host architecture;
- runs as a Linux OCI container under Podman;
- contains the configured shell and keepalive command;
- has a usable absolute home and work directory;
- behaves correctly under the selected security class.

This does not install or boot Windows, macOS, Android, or a second kernel.
Windows container images do not run on the Linux Podman target used by
`cagent`.

## Add another Linux distribution

There is no separate OS installer. Choose a Linux OCI base image and either use
it directly through a pull profile or build a tool image from it. The runtime
contract matters more than the distribution label.

| Distribution family | Package tooling | Common shell | Typical CA bundle |
| --- | --- | --- | --- |
| Alpine | `apk` | `/bin/sh` or installed `/bin/bash` | `/etc/ssl/certs/ca-certificates.crt` |
| Debian/Ubuntu | `apt-get` | `/bin/bash` | `/etc/ssl/certs/ca-certificates.crt` |
| Fedora/Rocky/RHEL-compatible | `dnf` or `microdnf` | `/bin/bash` | `/etc/pki/tls/certs/ca-bundle.crt` |
| Nix | Nix profile/image tooling | Image-dependent | Image-dependent |

For every new base:

1. Select a tag and resolve its immutable manifest digest.
2. Confirm the digest includes the target CPU architecture.
3. Install the shell, certificates, and tools required by the profile.
4. Create a non-root user when the image supports it.
5. Create and own the work directory.
6. Set `USER` and `WORKDIR`, or declare the corresponding runtime contract.
7. Verify the image under `sandbox` before selecting broader policy.
8. Add deterministic profile health checks.

The built-in [Ubuntu](../profiles/builtin/ubuntu-implementation/Containerfile),
[Debian](../profiles/builtin/debian-agent/Containerfile), and
[Fedora](../profiles/builtin/fedora-agent/Containerfile) Containerfiles are
working templates for those package-manager families. Do not copy an old digest
blindly; use the reviewed pin/update process described in
[Container image pins](IMAGE_PINS.md).

## Choose the correct image path

| What you have | Recommended path |
| --- | --- |
| A remote image needed once | `cagent run --image ...` |
| A remote image reused by environments | Custom profile with `image.mode: pull` |
| An image already present in rootless Podman | Custom profile with `image.mode: existing` |
| An OCI or Docker archive file | `podman load`, then an `existing` profile |
| A Dockerfile or Containerfile | `env init --containerfile ... --context ...` |
| A multi-stage or build-argument profile | Manual custom profile with `image.mode: build` |

## Use a remote image once

Use a fully qualified, immutable reference when possible:

```bash
cagent run \
  --image docker.io/library/alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce \
  --network none \
  -- /bin/sh
```

To inspect a project safely:

```bash
cagent run \
  --image IMAGE@sha256:DIGEST \
  --project "$PWD" \
  --project-mode read-only \
  --network none \
  -- /bin/sh
```

The raw container is removed on exit. This path does not create a profile or
environment manifest.

## Create a reusable pull profile

Custom profiles live in a directory whose name must match `metadata.name`:

```text
${XDG_CONFIG_HOME:-$HOME/.config}/containersagents-v2/profiles/PROFILE/profile.json
```

The following Alpine profile uses a pinned image and container-root identity.
Container root is mapped inside Podman's rootless user namespace and is not
host root.

```bash
profile_root="${XDG_CONFIG_HOME:-$HOME/.config}/containersagents-v2/profiles/alpine-tools"
mkdir -p "$profile_root"
```

Create `profile.json` in that directory:

```json
{
  "apiVersion": "containersagents.dev/v2alpha1",
  "kind": "Profile",
  "metadata": {
    "name": "alpine-tools"
  },
  "spec": {
    "image": {
      "mode": "pull",
      "reference": "docker.io/library/alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce",
      "pullPolicy": "missing"
    },
    "runtime": {
      "user": "root",
      "home": "/root",
      "workdir": "/workspace",
      "shell": ["/bin/sh"],
      "keepalive": ["sleep", "infinity"],
      "identityMode": "rootless-container-root"
    },
    "features": ["alpine-tools"],
    "defaults": {
      "securityClass": "sandbox",
      "resourceClass": "battery",
      "rootfsPersistence": "ephemeral"
    },
    "minimumResources": {
      "memoryMiB": 256,
      "cpus": 1,
      "pids": 256,
      "shmMiB": 64
    },
    "checks": [
      {
        "name": "alpine",
        "command": ["/bin/sh", "-c", "test -r /etc/alpine-release"],
        "timeoutSeconds": 10
      }
    ]
  }
}
```

Validate, pull, and use it:

```bash
cagent profile validate alpine-tools
cagent profile build alpine-tools

mkdir -p "$HOME/Development/alpine-lab"
cagent env init alpine-lab \
  --profile alpine-tools \
  --project "$HOME/Development/alpine-lab" \
  --security sandbox \
  --rootfs ephemeral

cagent env plan alpine-lab
cagent env shell alpine-lab
```

### Pull policies

| Policy | Intended meaning |
| --- | --- |
| `missing` | Pull only when the exact reference is absent. Recommended with a digest. |
| `always` | Ask Podman to pull every time the image is applied or built. |
| `newer` | Ask Podman to check whether a tagged reference is newer. |
| `never` | Never pull. Normally pair with `existing` mode. |

For release profiles, use `registry/repository:tag@sha256:digest` and `missing`.
A tag remains readable while the digest supplies immutable identity.

## Use an image already in Podman

First create or load the image as the same rootless user that runs `cagent`:

```bash
podman pull registry.example.com/team/toolbox:2026.07
podman image inspect registry.example.com/team/toolbox:2026.07
```

Create a custom profile with:

```json
{
  "apiVersion": "containersagents.dev/v2alpha1",
  "kind": "Profile",
  "metadata": {
    "name": "team-toolbox"
  },
  "spec": {
    "image": {
      "mode": "existing",
      "reference": "registry.example.com/team/toolbox:2026.07",
      "pullPolicy": "never"
    },
    "runtime": {
      "user": "1000",
      "home": "/home/agent",
      "workdir": "/workspace",
      "shell": ["/bin/sh"],
      "keepalive": ["sleep", "infinity"],
      "identityMode": "explicit"
    },
    "defaults": {
      "securityClass": "development",
      "resourceClass": "balanced",
      "rootfsPersistence": "persistent"
    },
    "checks": [
      {
        "name": "shell",
        "command": ["/bin/sh", "-c", "exit 0"]
      }
    ]
  }
}
```

`existing` mode never downloads or builds. `cagent profile build team-toolbox`
only verifies that the reference exists. If another rootless user or rootful
Podman owns the image, `cagent` will not see it.

For production, prefer a digest reference. A local mutable tag is appropriate
for development only.

## Load an OCI or Docker image archive

Podman can load OCI archives and Docker archives into local container storage.
Use the official [`podman load` documentation](https://docs.podman.io/en/stable/markdown/podman-load.1.html)
for supported formats and remote-client restrictions.

Load the archive as the normal rootless user:

```bash
podman load --input ./team-toolbox.tar
podman image list
```

If necessary, assign a stable local name:

```bash
podman image tag LOADED_IMAGE_ID localhost/team-toolbox:2026.07
```

Then use an `existing` profile whose `reference` is
`localhost/team-toolbox:2026.07`.

An archive produced by `podman save` or `docker save` is an image archive.
A filesystem archive produced by `podman export` is different; it must be
imported with Podman before `cagent` can use the resulting image.

## Build from a Dockerfile or Containerfile

Podman accepts Dockerfile and Containerfile syntax. The current
[`podman build` documentation](https://docs.podman.io/en/stable/markdown/podman-build.1.html)
describes their compatibility. `cagent` always passes the selected file
explicitly, so it can be named `Dockerfile`, `Containerfile`, or another name.

Create a dedicated build context:

```text
container/
├── Containerfile
└── .containerignore
```

Example `Containerfile`:

```dockerfile
FROM docker.io/library/alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce

RUN apk add --no-cache bash ca-certificates curl git jq \
    && addgroup -g 1000 agent \
    && adduser -D -u 1000 -G agent -s /bin/bash agent \
    && mkdir -p /workspace \
    && chown agent:agent /workspace

USER agent
WORKDIR /workspace
```

Example `.containerignore`:

```text
.git
.env
*.pem
*.key
*.p12
*.pfx
credentials.json
```

Optionally inspect the file-derived draft:

```bash
cagent profile detect \
  --containerfile ./container/Containerfile \
  --context ./container \
  --name alpine-agent
```

`profile detect` prints an untrusted draft. It does not install a profile.
Review its user, home, work directory, shell, keepalive, and identity before
using those values.

Create an environment and its custom build profile in one operation:

```bash
mkdir -p "$HOME/Development/alpine-agent-dev"
cagent env init alpine-agent-dev \
  --containerfile ./container/Containerfile \
  --context ./container \
  --project "$HOME/Development/alpine-agent-dev" \
  --shell /bin/bash \
  --user agent \
  --home /home/agent \
  --workdir /workspace
```

This creates a custom profile named `alpine-agent-dev-local`. The stored
profile refers to the absolute Containerfile and context paths; it does not
copy the build context into V2 configuration.

Then plan or build:

```bash
cagent profile validate alpine-agent-dev-local
cagent profile build alpine-agent-dev-local
cagent env plan alpine-agent-dev
cagent env shell alpine-agent-dev
```

## Build-context requirements

Every `cagent` build path enforces these rules:

- the context is explicit and exists;
- the selected file is inside the context;
- `.containerignore` exists;
- the context is no larger than 2 GiB;
- `.env`, key, certificate, credential, and similar suspicious files must be
  excluded;
- the full host home cannot be the context;
- build arguments with secret-like names are rejected;
- declared build secrets fail closed because an external provider is not yet
  implemented.

Keep the context narrow. A project root can be a context only when it is
deliberately curated; a dedicated `container/` directory is easier to review.

## Create an advanced manual build profile

Use a manual profile when you need a multi-stage target or non-secret build
arguments. Place `profile.json`, the Containerfile, and context files together:

```text
~/.config/containersagents-v2/profiles/custom-dotnet/
├── profile.json
├── Containerfile
└── .containerignore
```

Example image section:

```json
{
  "image": {
    "mode": "build",
    "repository": "localhost/containersagents-v2/custom-dotnet",
    "containerfile": "Containerfile",
    "context": ".",
    "pullPolicy": "newer",
    "target": "development",
    "buildArgs": {
      "DOTNET_CONFIGURATION": "Debug"
    }
  }
}
```

The repository must not include a tag or digest; `cagent` adds a tag derived
from the profile manifest and build-context hash. Changing either creates a new
image identity and makes existing environments report drift.

Do not place credentials in `buildArgs`. The current release intentionally
rejects secret-like argument names and does not yet provide a build-secret
provider.

## Runtime contract checklist

Before treating an image as compatible, verify:

1. **Architecture:** The manifest includes the host architecture. The
   `pwsh-dotnet` built-in is currently Linux amd64 because its upstream
   PowerShell image lacks Linux arm64.
2. **Shell:** `spec.runtime.shell[0]` exists and runs as the configured user.
3. **Keepalive:** The keepalive process stays in the foreground. The standard
   profiles use `sleep infinity`.
4. **Work directory:** The absolute path exists or can be used as the project
   mount target.
5. **Home:** The absolute path is not `/` and is usable by the runtime user.
6. **Identity:** Choose the least surprising identity mode.
7. **Read-only operation:** Images used with `sandbox` or `read-only` rootfs do
   not require writes outside the supplied temporary paths and mounts.
8. **Health checks:** Commands are non-interactive, deterministic, and finish
   within their timeout.
9. **CA bundle:** Set `runtime.caBundle` only when that file exists in the image
   and should be the base for certificate injection.

### Identity modes

| Mode | Use it when |
| --- | --- |
| `managed-user` | The image deliberately contains the declared positive UID/GID and user. Built-ins use rootless `keep-id` mapping. |
| `image-user` | The image's configured user should be used without an explicit `--user`. |
| `explicit` | Podman should run the declared user name or numeric ID. |
| `rootless-container-root` | The image expects container root; this maps to the normal host user through rootless Podman. |

Container root is less isolated inside the container than a non-root image
user, even though it is not host root. Prefer a non-root image user when the
image supports it.

## Private registries

Authenticate Podman as the same normal user before `cagent` pulls or builds:

```bash
podman login registry.example.com
podman pull registry.example.com/team/toolbox@sha256:DIGEST
```

See the official [`podman login` documentation](https://docs.podman.io/en/stable/markdown/podman-login.1.html)
for authentication-file behavior. `cagent` does not accept registry passwords,
copy authentication into manifests, or mount registry credentials into the
managed container.

Use the fully qualified registry name and immutable digest in the profile.
Avoid disabling TLS verification; configure the registry trust and certificate
chain at the Podman host level instead.

## Image update procedure

For a pull profile:

1. Resolve and review the new upstream tag and manifest-list digest.
2. Confirm the manifest contains every supported architecture.
3. Update the readable tag and digest together.
4. Run `cagent profile validate PROFILE`.
5. Run `cagent profile build PROFILE`.
6. Plan every dependent environment.
7. Review drift and recreate explicitly.
8. Run `cagent env doctor ENVIRONMENT`.

For a build profile, update the `FROM ...@sha256:...` line and repeat the same
validation. Built-in image maintenance is documented in
[Container image pins](IMAGE_PINS.md).

## Common failures

### `profile ... does not exist`

Confirm the path is `profiles/NAME/profile.json`, the directory is under the
resolved XDG configuration root, and `metadata.name` exactly matches `NAME`.

### Image reference is unavailable

- `pull` mode: verify registry access and authentication.
- `existing` mode: pull, load, import, or build the reference first as the same
  rootless user.
- Digest reference: confirm the digest belongs to the named repository and has
  a compatible platform.

### Shell or keepalive fails

Run the image directly without project data first:

```bash
cagent run --image IMAGE --network none -- /path/to/shell -c 'id; pwd'
```

Correct the runtime user, shell, home, work directory, or keepalive in the
profile; do not weaken the environment security class to hide an image error.

### Dockerfile builds with Docker but not cagent

Check `.containerignore`, the 2 GiB limit, base-image platform availability,
rootless build compatibility, secret-like build arguments, and whether the
Dockerfile assumes a Docker daemon or socket. `cagent` uses rootless Podman and
will not expose an engine socket.

### Image requires privileged mode

It is incompatible with the current safety model. `cagent` does not provide a
privileged override, host namespace sharing, `ALL`, or `SYS_ADMIN`.
