# Documentation

ContainersAgents V2 is a Linux-first manager for secure, single-container
development environments backed by rootless Podman. Start with the document
that matches what you are trying to do.

## User documentation

| Document | Use it for |
| --- | --- |
| [Full usage guide](USAGE_GUIDE.md) | Installing `cagent`, configuring defaults, creating environments, daily lifecycle operations, policy, persistence, networking, secrets, troubleshooting, and automation. |
| [Usage cheat sheet](CHEATSHEET.md) | A short command-oriented reference for normal daily work. |
| [Other operating systems and images](IMAGES_AND_OTHER_OS.md) | Using another Linux distribution, an OCI/Docker image, a local Podman image, or a Dockerfile/Containerfile. It also explains host OS support. |
| [CLI reference](CLI.md) | Every command and its important options. |

## Design and operations

| Document | Use it for |
| --- | --- |
| [Security model](SECURITY_MODEL.md) | Trust boundaries, security classes, hard denials, and explicit exceptions. |
| [Architecture](ARCHITECTURE.md) | Control flow, ownership, state, and package boundaries. |
| [Container image pins](IMAGE_PINS.md) | Built-in image digests, supported architectures, and the refresh procedure. |
| [Implementation status](IMPLEMENTATION_STATUS.md) | Implemented scope, deferred work, and release verification. |

## Schemas and examples

- [`schemas/profile-v1.schema.json`](../schemas/profile-v1.schema.json) — profile
  manifests.
- [`schemas/environment-v1.schema.json`](../schemas/environment-v1.schema.json)
  — environment manifests.
- [`schemas/defaults-v1.schema.json`](../schemas/defaults-v1.schema.json) — local
  defaults.
- [`examples/`](../examples) — defaults, environments, security policies, and a
  custom build profile.

The current API is `containersagents.dev/v2alpha1`. Examples in this repository
are templates: replace host paths and secret names before using them.
