# Cobra CLI Design

**Date:** 2026-07-26

**Status:** Approved direction; implementation contract

## Purpose

Stacks has outgrown its handwritten argument router. It now has ten
application command identities, three nested command families, seven review
actions, command-specific flags, and manually maintained usage errors.

This change adopts Cobra as the CLI transport boundary. Cobra will own command
discovery, help, positional validation, and command flags. Existing
configuration validation, dependency composition, services, privacy
boundaries, and domain behavior remain authoritative.

Viper and configuration files are intentionally excluded. They will be a
separate change after the Cobra migration is reviewed and merged.

## Command Tree

```text
stacks
├── (no arguments)                         # serve
├── serve
├── auth
│   ├── google
│   └── google-directory
├── doctor
├── sync
├── entities
│   ├── list
│   └── show <entity-id>
├── review
│   ├── list
│   ├── show <proposal-id>
│   ├── accept <proposal-id> <entity-id>
│   ├── accept-directory <proposal-id> <directory-profile-id>
│   │   └── [--entity <entity-id>]
│   ├── reject <proposal-id>
│   ├── create <proposal-id>
│   │   ├── --name <name>
│   │   └── [--email <email>]
│   └── correct <effective-decision-id> <entity-id>
├── analyze
├── db-migrate
├── db-status
└── db-reset delete-local-stacks-postgres
```

The root command remains runnable so no arguments continue to start the
server. `stacks serve` remains the explicit equivalent. Both forms reject
positional arguments.

`analyze` remains a temporary specialized client of the temporal engine. Its
presence in the command tree does not make manager-confidence an installation
mode, database scope, or permanent product boundary.

## Architecture

`internal/cli` will construct a fresh Cobra root for every execution. No
package-global command or flag state may be reused.

The Cobra tree will be declarative until a leaf command executes. Each leaf
will translate already-parsed positional arguments and flags into a typed,
action-specific callback. The callbacks will adapt to the existing command and
service implementations; Cobra types will not cross into application,
provider, storage, public core, or PostgreSQL adapter contracts.

`internal/app` will remain the lifecycle boundary:

1. identify the selected application command from the leaf callback;
2. run the existing `Settings.Validate(command)` contract;
3. only after validation, ask the command provider for the selected live
   dependencies;
4. execute the existing command or service behavior;
5. return the original error identity to the process boundary.

Help, completion, unknown-command handling, and syntax errors must not
construct application providers. Invalid command-specific configuration and
invalid CLI input must both fail before any live PostgreSQL, Google, AWS,
directory, or model boundary is constructed. CLI syntax may be validated
before command-specific configuration; the security and lifecycle invariant is
that neither failure path constructs live dependencies.

`cmd/stacks` remains dependency composition only. It may supply lazy callbacks
or factories, but it must not contain Cobra routing policy or business logic.

## Parsing and Help

Cobra owns all user-facing command syntax:

- root and nested `--help`;
- exact positional arity;
- `review create --name` and optional `--email`;
- `review accept-directory` and optional `--entity`;
- unknown flags and unexpected arguments;
- generated usage and command suggestions;
- Cobra's standard completion support.

The old `flag.FlagSet` parsers and handwritten aggregate usage strings will be
removed. Leaf callbacks receive typed values and must not parse the same input
again.

Only double-dash long flags are documented and supported:

```text
--name
--email
--entity
```

Single-dash long spellings such as `-name` are not a compatibility contract.

Required string flags must be nonblank after trimming. In particular:

- `review create --name ""` is invalid;
- an omitted `review accept-directory --entity` means no existing entity was
  selected;
- an explicitly blank `--entity ""` is invalid.

The existing exact `db-reset` confirmation token remains mandatory. Cobra
validates arity, while the guarded resetter remains the final authority for
the token and destructive-scope checks.

## Output, Errors, and Privacy

The root command will receive explicit stdin, stdout, and stderr writers.
Private entity, review, transcript, and analysis content remains confined to
the existing explicit stdout boundary.

Cobra will use:

- `SilenceErrors: true`, because `main` already owns final error reporting;
- `SilenceUsage: true`, so provider, database, and domain failures never emit
  unrelated usage text.

Help is requested explicitly with `--help` and is written through the
configured output writer. Syntax errors are returned as bounded,
privacy-safe errors. Positional validators report expected counts or required
identifiers without echoing private argument values.

Underlying cancellation, provider, storage, and domain errors must remain
discoverable with `errors.Is`. Cobra callbacks return those errors without
stringifying or replacing them.

## Dependency Policy

The root module will add `github.com/spf13/cobra` at `v1.10.2`, the latest
stable version reported by the Go module proxy when this design was written.
Its transitive graph will be reviewed when the module files change.

Cobra is an intentional dependency exception because it replaces concrete,
growing CLI machinery. It is restricted to the root application's CLI
transport boundary:

- no Cobra dependency in `core`;
- no Cobra dependency in `adapters/postgres`;
- no `cobra-cli` generator;
- no global mutable Cobra singleton;
- no remote command registry, plugin framework, reflection, or generated
  command scaffolding.

`AGENTS.md` will record this approved boundary so future automated cleanup does
not remove Cobra merely because the repository generally prefers the standard
library.

## Deliberate Behavior Changes

The migration intentionally changes these behaviors:

- `stacks --help` and nested help become supported;
- usage is generated from the command tree rather than handwritten errors;
- invalid nested commands receive Cobra's bounded command error and
  suggestions;
- `stacks serve` rejects extra arguments instead of ignoring them;
- flags use documented double-dash long syntax;
- command parsing and help can complete without command-specific provider
  construction.

Operational output, persistent state transitions, provider selection,
configuration requirements, disclosure policy, database guards, and
provenance behavior do not change.

## Testing

Implementation follows test-driven development. Each behavior is first
expressed as a failing public-boundary test.

The required matrix covers:

1. no-argument and explicit serving;
2. deterministic root and nested help without provider construction;
3. unknown commands and nested commands without provider construction;
4. exact positional arity for every leaf;
5. `review create` required/nonblank name and optional email;
6. `review accept-directory` omitted-versus-blank entity behavior;
7. target-specific auth construction;
8. database-command isolation and exact reset confirmation;
9. command-specific configuration failure before live dependencies;
10. private output confinement and single-owner error reporting;
11. preservation of cancellation and wrapped error identity;
12. fresh-tree execution without retained flags or writers.

The completion gate is:

```sh
make fmt
make test
make test-race
make staticcheck
make build
make modules-check
make test-integration
git diff --check
```

Because command composition and database-command timing are in scope, the
documented PostgreSQL-gated integration target will run against the local
OrbStack PostgreSQL environment through the existing ignored `.env`. No Google
Drive, Workspace Directory, Bedrock, Anthropic, or OpenAI call is part of this
change.

## Follow-on Viper Boundary

After this Cobra change is merged, a separate design and PR may add an explicit
`--config` root option backed by a constrained Viper loader. That later change
will keep secrets environment-only, accept only explicit YAML or JSON files,
preserve typed Stacks validation, and define precedence as flags over
environment over file over defaults.

No part of that configuration-source behavior is implemented here.
