# Implementation status

The current release is `0.1.0-alpha.1` and implements the Linux-first,
single-container `containersagents.dev/v2alpha1` MVP.

## Implemented

- One static Go binary with no third-party Go modules and direct Podman CLI
  invocation.
- Profile, environment, and defaults manifests with embedded JSON schemas and
  five built-in profiles.
- XDG-based desired and applied state, stable environment UUIDs, atomic writes,
  and global/environment/project/build locks.
- Rootless Podman capability detection, adaptive CPU and memory budgets,
  aggregate-memory checks, and battery/balanced/performance/custom classes.
- Deterministic planning, content and spec hashes, drift reporting, explicit
  recreation, and ownership-checked lifecycle operations.
- Sandbox, development, integration, and elevated-lab security classes;
  canonical mount validation; secrets, certificates, proxy settings, ports,
  and selected capabilities.
- Persistent or ephemeral homes and root filesystems, project concurrency
  controls, internal networks, shell/exec sessions, and raw OCI image runs.
- V2-scoped disk reporting and cleanup, bounded audit logs, health checks,
  machine-readable output, and shell completion.
- Unit tests for parsing, manifests, hashing, profile storage, planning, Podman
  arguments, resources, mounts, state, and lifecycle behavior; an opt-in full
  rootless-Podman lifecycle integration test and matching CI job.

## Deliberately deferred

- Multi-container stacks and service dependency orchestration.
- Remote Podman hosts and shared controller services.
- Feature/layer composition across profiles.
- Domain-aware egress allowlists or an enforcing network proxy.
- GUI, web API, and background reconciliation.
- External build-secret providers. Profiles may declare build-secret intent,
  but builds fail closed until a provider is configured.

## Verification

Run `make check` for unit tests, vet, and JSON validation, then `make build`.
Run `make coverage` to enforce the current application and Podman-adapter
regression floors.
On a rootless Podman host with cgroups v2, `make integration` additionally runs
the raw-image and managed-environment lifecycle tests. Set
`CAGENT_INTEGRATION_IMAGE` to a locally available image by immutable digest when
the host must not pull the default pinned Alpine image.

Release binaries must be built with the security-patched toolchain selected by
the `toolchain` directive in `go.mod`, or a newer supported Go release.
