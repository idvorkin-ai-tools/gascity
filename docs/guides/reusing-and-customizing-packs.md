---
title: "Reusing and Customizing Packs"
description: Bring a reusable pack into your city, keep it cached locally, and customize behavior without forking.
---

# Reusing and Customizing Packs

Cities are the place where the work happens in Gas City. Packs define what a
city can do and how it does it.

A pack is a named collection of city-building definitions: agents, named
sessions, formulas, skills, commands, patches, and the files those definitions
need at runtime. Cities are built from one or more packs.

Every city has at least one pack: the city's root pack. The root pack is the
same directory your `city.toml` file is in. The things your pack can do are
stored in the `pack.toml` file that sits next to `city.toml`, plus files in a
few well-known directories.

A city is implicitly built on its root pack. Packs themselves, including the
root pack, can be built on other packs. To add another pack, use `gc pack add`.

For example, this command imports the `gascity` pack into your city under the
local name `gascity`:

```bash
#!/usr/bin/env bash
# Add the registry pack under the local import binding "gascity".
gc pack add main:gascity --name gascity
```

That command writes an import to `pack.toml`:

```toml
[imports.gascity]
source = "https://packages.example/main/gascity"
```

When Gas City loads this pack, it also loads the imported pack. If `gascity`
defines a city-level agent named `reviewer`, the current PackV2 loader exposes
that agent by its local runtime name:

```bash
#!/usr/bin/env bash
# Send work to the reviewer agent from the imported gascity pack.
gc sling reviewer <bead-id>
```

The import binding, `gascity`, is local to the importing TOML file. It is useful
for dependency management and deterministic loading, but it is not currently a
runtime namespace for agents. If two imported packs define the same non-fallback
agent name on the same surface, loading fails instead of silently choosing one.

When your city starts, Gas City resolves the root pack plus every pack it
imports into one effective city configuration.

That means pack reuse is not a separate mode from normal city configuration. It
is the way Gas City lets one city or pack build on work that another pack has
already packaged, named, tested, and published.

This guide uses the `gascity` pack as the default example. The examples focus
on reusing an agent because agents are the easiest surface to see in a running
city. The same import model also applies when a pack primarily shares formulas,
skills, commands, MCP configuration, patches, or some combination of those
surfaces.

## What A Pack Is

Physically, a pack is a directory with a `pack.toml` file, well-known
definition directories, and private assets. PackV2 keeps implementation files
such as prompts and scripts under assets instead of treating them as top-level
pack structure.

```text
gascity/
  pack.toml
  assets/
  commands/
  doctor/
  formulas/
  mcps/
  namepools/
  orders/
  overlay/
  prompts/
  scripts/
  skills/
```

Conceptually, a pack has three parts:

- **Metadata** tells Gas City the pack name, schema version, and compatibility
  expectations. Metadata lives in `pack.toml`.
- **Definitions** are the named things other users can run or build on, such as
  agents, named sessions, formulas, skills, commands, and MCP-related
  configuration. Imports, pack metadata, provider declarations, services,
  agent patches, commands, doctor checks, globals, agents, and named sessions
  live in `pack.toml`. Formula files and other subsystem-specific definition
  bodies live in well-known directories.
- **Assets** are private files those definitions need, such as prompt templates
  or setup scripts. Assets live under `assets/` and are opaque to Gas City
  except when a definition points at them.

The distinction matters because users should normally depend on named
definitions, not on a pack's private files. If the `gascity` pack exposes a
`reviewer` agent, you can use that agent without knowing which prompt file or
setup script implements it.

## Public Surface

A pack's public surface is the set of definitions another city or pack can
intentionally use. Think of it as the pack's API. The pack can contain many
private details, but only its public surface is something downstream users
should treat as stable.

For this guide, think about the public surface as agent-first, but not
agent-only:

- agents
- named sessions for those agents
- pack-level patches that shape those agents
- formulas, skills, commands, and MCP configuration the pack chooses to expose
- definitions from imported packs that the loader makes visible by scope

Naming is part of that API. When a pack publishes an agent named `reviewer`, it
is making a product promise: downstream users can refer to `reviewer` and expect
that name to keep meaning something coherent across compatible versions. The
versioning section below explains how to keep that promise stable while still
allowing updates.

## Importing Packs

Every pack you depend on must be represented by an explicit import. In
day-to-day use, create that import with `gc pack add` rather than by editing TOML
directly.

Local packs use the same `source` field. They usually omit `version` because
there is no registry or remote release to resolve:

```toml
[imports.local_agents]
source = "../packs/local-agents"
```

The `source` field is the durable place Gas City resolves the pack from.
`version` is the compatibility range for a versioned remote import. The cache is
separate: it answers "where did Gas City put the fetched copy on this machine?"
and should not become part of checked-in pack configuration.

Registry names are not durable dependency coordinates. `main:gascity` is a
convenient handle you can pass to `gc pack add`, but the import recorded in
`pack.toml` should be portable. It should use a durable `source` that points at
the pack root, plus a `version` constraint when the source is versioned. That
source can be a loose directory on disk, a remote repository, or a remote
repository plus a pack subdirectory inside a monorepo.

## Basic Flow

The pack registry is where users discover reusable packs. A registry is an
index, not the pack storage itself: it tells Gas City which packs exist, what
versions are available, and where each pack source lives.

Start by listing the registries this machine knows about:

```bash
#!/usr/bin/env bash
# Show the registries available to this machine.
gc pack registry list
```

Example output:

```text
NAME   URL
main   https://packages.gascityhall.com
```

Then search for the kind of surface you want. Here we are looking for a reusable
agent:

```bash
#!/usr/bin/env bash
# Search all configured registries for packs related to reviewers.
gc pack registry search reviewer
```

Example output:

```text
PACK          VERSION   SUMMARY
main:gascity  1.4.0     Gas City agents for review, triage, and coordination
main:triage   1.2.0     Lightweight issue and bead triage agents
```

The `main:` prefix is the registry name. It is useful while you are searching,
but it is not the dependency coordinate that gets written to `pack.toml`.

Inspect the result before adding it:

```bash
#!/usr/bin/env bash
# Show the registry record for the pack we plan to reuse.
gc pack registry show main:gascity
```

Example output:

```text
Pack: main:gascity
Version: 1.4.0
Source: https://packages.example/main/gascity
Definitions:
  agent reviewer
  agent triage
```

Now add the pack to your city:

```bash
#!/usr/bin/env bash
# Add the registry pack under the local import binding "gascity".
gc pack add main:gascity --name gascity

# Confirm that the city now has a gascity import.
gc pack list
```

Example output:

```text
NAME     SOURCE                                VERSION
gascity  https://packages.example/main/gascity 1.4.0
```

`gc pack add` records an explicit import in the selected pack, usually your
city's root pack. It also fetches the pack into the local cache. The cache keeps
day-to-day use feeling local; starting or validating a city should not feel like
fetching the internet every time.

After adding the pack, validate the resolved config and look for the agent you
want to use:

```bash
#!/usr/bin/env bash
# Validate the composed city configuration.
gc config show --validate

# Look for the imported reviewer agent in the resolved config.
gc config show | rg 'name = "reviewer"|reviewer'
```

If you later decide this city should stop using the pack, remove the import
through the same pack-management surface:

```bash
#!/usr/bin/env bash
# Remove the local import binding.
gc pack remove gascity

# Confirm that the binding is gone and the city still validates.
gc pack list
gc config show --validate
```

## Registry Handles And Portability

A fresh Gas City installation is expected to know about the Gas City-managed
`main` registry. Teams can also add their own registries for private or
organization-specific packs.

It helps to separate the three places a pack can appear. The registry is where
you find a pack. The import in `pack.toml` is how your city remembers the pack.
The cache is where Gas City keeps the fetched copy so normal use feels local.

That means a registry is not the cache, and it is not the only way to reuse a
pack. Local path imports and direct source imports are still normal pack
workflows. The registry is the convenience layer for discovery and for turning a
human-friendly handle into durable import configuration.

The important portability rule is: registry names belong to commands, not to
pack files. If two machines have different registry lists, the same checked-in
`pack.toml` should still describe the same dependency.

## Use An Agent From A Pack

After adding a pack, use the public names the loader exposes. If `gascity`
defines a city-level `reviewer` agent, the current runtime agent name is
`reviewer`.

```bash
#!/usr/bin/env bash
# Check the city and confirm the imported agent is available.
gc status

# Attach to the imported reviewer agent.
gc session attach reviewer

# Send work to the imported reviewer agent.
gc sling reviewer <bead-id>
```

Example output:

```text
Attached to reviewer
Created bead GC-1042 assigned to reviewer
```

Import bindings do not currently qualify runtime agent names. If two imported
packs define the same non-fallback city agent, Gas City reports a collision
rather than relying on load order. Resolve that by choosing compatible packs,
using fallback definitions where appropriate, or having the pack author publish
distinct runtime names.

## Choose The Import Name

Import bindings are local dependency names. If you add `main:gascity` as
`gascity`, the `pack.toml` dependency is easy to read:

```bash
#!/usr/bin/env bash
# Keep the imported pack's product name visible.
gc pack add main:gascity --name gascity
```

Example output:

```text
Added main:gascity as gascity
```

If you add it as `review`, only the local import binding changes. Current
runtime agent names do not automatically become `review/...`:

```bash
#!/usr/bin/env bash
# Record the same source under a different local import binding.
gc pack add main:gascity --name review
```

Example output:

```text
Added main:gascity as review
```

Choose the binding you want maintainers to see in TOML:

- Keep the original pack name when provenance is useful to users.
- Rename an import when the same source appears in multiple roles.
- Rename an import when the local pack is deliberately documenting a different
  relationship to the dependency.
- Do not rely on the binding to disambiguate runtime agent names.

When two imports define the same public runtime name, fix the pack definitions
or choose different packs. The current loader does not use the import binding
as a runtime namespace.

## Customize Without Forking

Most pack reuse should not start with a fork. If the imported pack is basically
the right product, customize it at the importing layer first.

Use a patch when you want local policy for one imported agent:

```toml
[[patches.agent]]
name = "reviewer"
provider = "codex"
idle_timeout = "45m"
option_defaults = { model = "sonnet", permission_mode = "plan" }
```

That patch says: "when this city uses the resolved `reviewer` agent, use these
local choices." The source pack still owns the reusable agent definition. The
local pack owns only the policy choice.

You can use another patch when you need to change one concrete resolved
definition:

```toml
[[patches.agent]]
name = "reviewer"
prompt_template = "assets/prompts/reviewer.md"
session_setup_append = ["tmux set status-left '[review]'"]
```

A patch targets the resolved definition directly. Reach for a patch when you are
replacing a prompt, appending setup behavior, changing provider options, or
changing another field on an imported agent.

People sometimes describe this as deriving from a pack. That mental model can
help if it reminds you that the source pack still has its own identity. But as a
user task, the practical choice is simpler: use patches for local policy and
forks only when you want to own a different product.

After changing patches, validate what Gas City resolves:

```bash
#!/usr/bin/env bash
# Validate the composed city configuration after the customization.
gc config show --validate

# Confirm that the resolved reviewer includes the choices we just made.
gc config show | rg 'name = "reviewer"|codex|idle_timeout'

# Check the running city after the config still validates.
gc status
```

Example output:

```text
Config OK
reviewer provider=codex idle_timeout=45m
City running
```

## Add A Pack From Another Pack

Packs can import other packs. That is how you build a product pack on top of
smaller reusable pieces.

From the product pack directory, add the reusable pack:

```bash
#!/usr/bin/env bash
# Add the reusable pack to the product pack in the current directory.
gc pack add main:gascity --name gascity

# Confirm the product pack now has that import.
gc pack list
```

Example output:

```text
NAME     SOURCE
gascity  https://packages.example/main/gascity
```

Or target the product pack explicitly:

```bash
#!/usr/bin/env bash
# Add the reusable pack to a specific pack directory.
gc pack add main:gascity --name gascity --pack ./packs/review-bundle
```

Example output:

```text
Added main:gascity as gascity in ./packs/review-bundle
```

The resulting pack config should read like this conceptually:

```toml
[pack]
name = "review-bundle"
schema = 1

[imports.gascity]
source = "https://packages.example/main/gascity"

[[patches.agent]]
name = "reviewer"
idle_timeout = "45m"
```

The import binding, `gascity`, is local to this pack. The pack can use that
binding in its own imports and pack-management flows without making that binding
a runtime namespace for its consumers.

## Future Export Surface

The `[export]` surface is specified as future/deferred behavior. It is useful
for understanding where pack reuse is going, but it is not implemented by the
current PackV2 loader. Do not put `[export]` tables in a pack you expect the
current loader to accept.

The intended product question is: how much of an imported pack should this pack
expose as part of its own public API? A future export surface may let a pack keep
one imported pack grouped under a namespace and present another imported pack as
if it belongs to the parent pack:

```toml
[pack]
name = "review-bundle"
schema = 1

[imports.gascity]
source = "https://packages.example/main/gascity"

[imports.triage]
source = "https://packages.example/main/triage"

[export.gascity]
as = "review"

[export.triage]
as = "."
```

Read that future example like this:

- `gascity` is imported and re-exposed under `review.*`.
- `triage` is imported and re-exposed as part of the parent pack's top-level
  surface.
- Imported surface without a matching `[export]` would remain internal.
- Every pack keeps its own identity and product stance.

For current PackV2, imported definitions are visible according to loader scope
rules, not according to `[export]`. If you see older PackV2 examples that use
`transitive`, inline import `export`, or `[export]`, treat them as transitional
or future-facing examples rather than current authoritative PackV2 syntax.

## Creating And Publishing Packs

You do not need to publish a pack to reuse one. But once you have a city pattern
that another city should be able to depend on, turn that pattern into a pack.

The basic authoring flow is:

```bash
#!/usr/bin/env bash
# Create a directory for the new product pack.
mkdir -p ./packs/review-bundle

# Write the pack metadata and first definitions.
$EDITOR ./packs/review-bundle/pack.toml

# Add the reusable pack that this product pack builds on.
gc pack add main:gascity --name gascity --pack ./packs/review-bundle

# Validate the city after adding the new pack dependency.
gc config show --validate
```

Example output:

```text
Added main:gascity as gascity in ./packs/review-bundle
Config OK
```

The first edit creates the pack metadata and the first definitions. The
`gc pack add` command records any dependency the new pack has on another pack.

Inside the pack, each public definition is a commitment. A stable agent name or
formula name becomes something downstream users can build against. Keep private
implementation files private, and expose the smallest surface that makes the
pack useful.

To publish a pack, you make its source available and add a catalog entry to a
pack registry. The Gas City-managed registry is the default place for broadly
useful public packs. A team can also run a third-party pack registry for private
or organization-specific packs. A third-party registry is still just a registry
catalog that points to pack sources; it does not need to be the place where the
pack source itself is hosted.

For a deeper authoring walkthrough, see [Shareable Packs](/guides/shareable-packs).

## Versioning And Updates

Added packs should be treated like dependencies. Use the registry and lockfile
to make updates explicit:

```bash
#!/usr/bin/env bash
# See which packs are installed and which version is resolved.
gc pack list

# Check whether the imported pack has an available update.
gc pack outdated gascity

# Upgrade the imported pack and refresh the lockfile.
gc pack upgrade gascity

# Validate the city after the dependency change.
gc config show --validate
```

Example output:

```text
NAME     SOURCE                                VERSION
gascity  https://packages.example/main/gascity 1.4.0

gascity  1.4.0 -> 1.5.0
Upgraded gascity to 1.5.0
Config OK
```

There are two versioning ideas to keep separate:

- `--version` is an authoring shortcut for the import's `version` constraint,
  such as `^1`, that tells Gas City which compatible release range you want when
  adding or upgrading a pack.
- The exact resolved release lives in `packs.lock`, not in the import block.

In `pack.toml`, the constraint is just another field on the import:

```toml
[imports.gascity]
source = "https://packages.example/main/gascity"
version = "^1"
```

For active development, a moving source can be useful. For shared cities,
templates, or published packs, prefer a version constraint and check in the
resulting lockfile so the city does not silently change behavior when the pack
source moves.

Before upgrading, ask what kind of promise the imported pack made:

- Patch releases should preserve the same public names and behavior.
- Minor releases can add new public surface without breaking existing users.
- Major releases can change the product stance or remove old public surface.

After upgrading, validate the resolved config and check the agents you actually
use. A pack upgrade changes city behavior, so treat it with the same care you
would give any dependency upgrade.

## When To Fork Instead

Fork a pack only when you are intentionally changing the shared default for
every downstream user, or when the imported pack no longer matches your product
stance.

If you are only tuning provider choices, prompts, timeouts, or one agent's
setup, prefer `gc pack add` and agent patches first.

## What This Guide Does Not Cover

This guide focuses on the user path for reusing and customizing packs. It does
not define the registry publishing policy, the final registry hosting workflow,
or every compatibility detail for older PackV2 import/export syntax.

Those details belong in the pack registry design notes, migration docs, and
reference pages. The main thing to remember here is the task flow: find a pack,
add it with `gc pack`, use its public surface, and customize locally without
forking unless you want to own a different product.

## See Also

- [Shareable Packs](/guides/shareable-packs)
- [CLI Reference](/reference/cli)
- [Config Reference](/reference/config)
