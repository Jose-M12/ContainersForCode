# Architecture

This document describes internals. For operational workflows, start with the
[full usage guide](USAGE_GUIDE.md) or [cheat sheet](CHEATSHEET.md).

ContainersAgents V2 is a local controller for isolated, single-container work
environments. The `cagent` binary owns desired state and invokes the rootless
Podman CLI directly. There is no daemon, socket integration, or background
reconciler.

## Control flow

1. A profile defines an image contract, runtime identity, shell, keepalive
   command, defaults, minimum resources, and optional validation checks.
2. An environment manifest selects a profile and declares project mounts,
   persistence, security, resources, network settings, secrets, and
   concurrency policy.
3. `cagent env plan` resolves defaults and the profile, validates host paths,
   compiles security and resource policy, inspects Podman, and produces a
   deterministic desired snapshot and action list.
4. `prepare`, `start`, `shell`, and `exec` apply that plan while holding the
   global, environment, and—where applicable—project locks.
5. Applied state records resource identifiers and hashes. Later plans compare
   desired, recorded, and actual state to identify drift.

The planner is read-only. Lifecycle code owns mutations and rechecks ownership
labels before deleting resources.

## State and ownership

Paths follow the XDG base-directory convention. The defaults file and desired
manifests live below the configuration root; registration state, audit logs,
persistent environment data, materialized built-in build contexts, and cached
host capability discovery use their corresponding state, data, and cache
roots. Run `cagent host doctor --output json` to see the resolved paths.

Each environment receives a UUID. Podman resources created by V2 carry
`io.containersagents.v2.managed=true` plus resource-specific ownership labels.
Container and network names include the environment identity. Cleanup and
delete operations use labels and recorded UUIDs rather than name matching
alone.

## Main packages

- `internal/app`: CLI dispatch, lifecycle orchestration, locking, confirmation,
  and audit integration.
- `internal/manifest`, `internal/profile`, `internal/envstore`: schema types,
  validation, defaults, and desired-state storage.
- `internal/planner`, `internal/policy`, `internal/resources`: deterministic
  plan construction and policy compilation.
- `internal/podman`, `internal/capability`, `internal/hostinfo`: Podman command
  adapter and host discovery.
- `internal/security`, `internal/fsutil`, `internal/locks`: mount boundaries,
  safe filesystem operations, and process locks.
- `internal/state`, `internal/audit`: applied-state index and per-environment
  JSON Lines audit records.

## Lifecycle boundaries

`env prepare` builds or pulls an image and creates owned resources without
starting the container. `env start` leaves the container running. `env shell`
and `env exec` start it when necessary and, by default, stop it on exit only if
that session performed the start. `env recreate` is the explicit path for a
destructive replacement after drift or immutable changes.

The `v2alpha1` MVP deliberately models one container per environment. Stacks,
remote Podman hosts, controller daemons, and feature composition are outside
this architecture.
