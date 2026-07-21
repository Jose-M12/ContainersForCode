# Security model

ContainersAgents V2 assumes a trusted local user, an untrusted or imperfect
container workload, and potentially sensitive customer project data. Its goal
is to reduce host exposure and make every exception visible; it is not a
virtual-machine security boundary.

## Baseline controls

- Podman must report rootless operation.
- No host, PID, IPC, or user namespace sharing is exposed by the manifests.
- Privileged containers, `SYS_ADMIN`, `ALL`, container-engine sockets, host
  root, and full host-home mounts are hard denials.
- Managed raw runs drop all capabilities, enable `no-new-privileges`, receive
  resource limits, publish no ports, and are removed on exit.
- Project and extra mount sources are canonicalized and checked against the
  configured allowed roots. Symlink resolution is part of validation.
- Secrets are references to Podman secrets and are mounted below
  `/run/secrets`; secret-like environment or build-argument names are rejected.
- Custom build contexts must be explicit, include `.containerignore`, remain
  below 2 GiB, and pass a suspicious-file scan.
- V2 only deletes resources bearing its management and ownership labels. It
  never invokes global Podman prune or reset commands.

## Security classes

`sandbox` forces a read-only root filesystem, drops capabilities, enables
`no-new-privileges`, uses restricted temporary filesystems, disables the
persistent home, makes the project read-only, and disables networking.

`development` is the normal implementation profile. Additional capabilities
remain forbidden. `noNewPrivileges` may be requested unless the profile
declares setuid-based sudo.

`integration` permits the environment's declared integration networking while
retaining the rootless and capability baseline.

`elevated-lab` is the only class that accepts explicitly listed capabilities.
`ALL` and `SYS_ADMIN` remain forbidden, and every added capability is recorded
as a policy exception.

## Explicit exceptions

The following operations require both a flag and an exact confirmation token:

- Mounting outside configured roots: `--allow-dangerous-mount --confirm NAME`.
- Publishing a non-loopback port: `--allow-external-port --confirm NAME`.
- Exceeding the aggregate V2 memory pool: `--allow-overcommit --confirm NAME`.
- A dangerous mount in `cagent run`: `--allow-dangerous-mount --confirm raw-run`.
- Recreate, delete, and custom-profile removal: `--confirm` with the exact
  environment or profile name.
- Applying managed disk cleanup: `--managed --confirm managed`.

These flags cannot override hard denials. Planning can show exceptions without
confirmation; commands that apply them require the token.

## Persistence and audit

Per-environment homes and generated certificate bundles are stored in private
V2 data directories. Environment deletion retains this data unless
`--delete-home` is explicitly selected. Audit logs record commands, planned
actions, hashes, resource classes, policy exceptions, confirmations, outcomes,
and owned resource IDs. Secret values are neither accepted in manifests nor
written to audit output.

## Operational guidance

Run `cagent host doctor` before first use and after Podman upgrades. Review every
plan, especially destructive actions and policy exceptions. Prefer read-only
project mounts for diagnostics, use `sandbox` for unknown images, provision
secrets through Podman rather than environment variables, and inspect
`cagent env diff` before recreating drifted environments.
