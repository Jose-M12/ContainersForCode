# CLI reference

For workflow explanations and examples, use the
[full usage guide](USAGE_GUIDE.md). For a compact daily reference, use the
[cheat sheet](CHEATSHEET.md). Custom image workflows are documented in
[Other operating systems and images](IMAGES_AND_OTHER_OS.md).

All commands return exit code `0` on success. Usage errors return `2`, policy
denials return `3`, missing required resources return `4`, and runtime or health
failures return `1`. Commands that support `--output` accept `human` or `json`.
Options may appear before or after positional arguments; use `--` before a
command executed inside a container.

## Profiles

| Command | Purpose and important options |
| --- | --- |
| `profile list [--output FORMAT]` | List built-in and custom profiles. |
| `profile show NAME [--output FORMAT]` | Show the resolved image contract and hash. |
| `profile validate NAME [--output FORMAT]` | Validate and resolve a profile. |
| `profile detect --containerfile FILE --context DIR [--name NAME]` | Emit an untrusted draft inferred from a Containerfile. |
| `profile build NAME [--force] [--output FORMAT]` | Pull or build the content-addressed profile image. |
| `profile remove NAME --confirm NAME` | Remove an unreferenced custom profile; built-ins cannot be removed. |

## Environments

`env init NAME` requires `--project DIR` and exactly one of `--profile NAME` or
`--containerfile FILE --context DIR`. It also accepts `--project-mode`,
`--security`, `--resource`, and `--rootfs`. Custom profile creation accepts
`--shell`, `--keepalive`, `--user`, `--home`, and `--workdir`.

```text
cagent env init NAME --profile PROFILE --project DIR
cagent env init NAME --containerfile FILE --context DIR --project DIR
```

| Command | Purpose and important options |
| --- | --- |
| `env list [--output FORMAT]` | List desired environments and observed state. |
| `env show NAME [--output FORMAT]` | Show desired, resolved, recorded, and available actual state. |
| `env plan NAME [PLAN OPTIONS]` | Compute actions without mutation. |
| `env prepare NAME [PLAN OPTIONS]` | Build/pull and create resources without starting. |
| `env start NAME [PLAN OPTIONS]` | Apply and leave the container running. |
| `env shell NAME [PLAN OPTIONS]` | Apply, start if needed, and open the profile shell. |
| `env exec NAME [PLAN OPTIONS] [--tty] -- COMMAND...` | Apply and execute a command; without a command, open the profile shell. |
| `env stop NAME` | Stop one owned container. |
| `env stop --all` | Stop all running V2-managed containers. |
| `env diff NAME` | Compare the applied snapshot with current desired and actual state. |
| `env doctor NAME` | Run ownership, drift, runtime, and profile checks. |
| `env recreate NAME --confirm NAME [PLAN OPTIONS]` | Explicitly replace the owned container for immutable changes or drift. |
| `env delete NAME --confirm NAME [--delete-home] [--output FORMAT]` | Delete desired and runtime resources, retaining persistent data by default. |

Plan options are `--resource CLASS`, `--allow-dangerous-mount`,
`--allow-external-port`, `--allow-overcommit`, `--confirm NAME`, and
`--output FORMAT` where supported. Applying any `--allow-*` option requires the
exact environment name as confirmation.

## Raw images

```text
cagent run --image IMAGE [--project DIR] [--project-mode read-only|read-write]
  [--network none|outbound] [--resource CLASS] [--tty=false]
  [--allow-dangerous-mount --confirm raw-run] -- COMMAND...
```

The run is ephemeral and auto-removed. A project mount is optional and defaults
to read-only. The default resource class is `battery`. Raw runs directly use
the adaptive `battery`, `balanced`, or `performance` classes; custom resource
values require a managed environment manifest.

## Host and disk

| Command | Purpose and important options |
| --- | --- |
| `host doctor [--refresh] [--output FORMAT]` | Discover Podman, cgroups, runtime, storage, architecture, resources, and rootless prerequisites. |
| `host off-check [--all-podman] [--output FORMAT]` | Return nonzero when matching containers are still running. The default scope is V2 only. |
| `disk report [--output FORMAT]` | Report V2-owned images, containers, networks, data, and cache. |
| `disk cleanup [--plan] [--output FORMAT]` | Show orphaned V2-owned resources; this is the default when `--managed` is absent. |
| `disk cleanup --managed --confirm managed [--output FORMAT]` | Apply the ownership-checked V2-only cleanup plan. |

## Utility commands

`version [--output human|json]` reports build metadata. `completion bash`,
`completion zsh`, and `completion fish` emit basic top-level completion scripts.
