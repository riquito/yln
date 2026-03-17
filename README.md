# yln — smart yarn linker

Links monorepo workspace packages into an app's `node_modules/` via symlinks,
automatically resolving transitive `workspace:*` dependencies. Unlike `yarn link --all`,
it only links the packages you need.

## Installation

Download the latest release from https://github.com/riquito/yln/releases
or build it yourself

```sh
go build -o yln .
```

Don't forget to add it somewhere in your `$PATH` for easy usage, e.g.

```sh
ln -s $PWD/yln ~/.local/bin
```

```sh
https://github.com/riquito/yln/releases

## Configuration

It's suggested to use a configuration file to define a alias to the monorepo you work with

Set a default monorepo path in `~/.config/yln/config.toml`:

```toml
monorepo = "~/projects/my-monorepo"
alias = "mono"  # optional: shown in status/check output as <mono>/packages/...
```

Without an alias, `yln status` uses the directory basename (e.g. `<my-monorepo>/packages/pkg-a`).

If you don't do that, you will have to add `--monorepo <path>` to most of the commands.

## Usage

```sh
# Link specific packages (from the app directory)
yln add some-pkg
# Links some-pkg AND its transitive workspace deps, but nothing else

# Dry run: preview what would be linked without making changes
yln add --dry-run some-pkg

# Remove specific package links
yln rm some-pkg

# Interactive picker (TUI with fuzzy search — space to toggle, enter to confirm)
# Despite the name you use just this one to add/remove packages
yln edit

# Show currently linked packages
yln status

# Remove all symlinks
yln clean

# Check for yln symlinks (exit 1 if any found — useful in CI)
yln check

# Stash current links (saves them and removes symlinks)
yln stash

# Restore previously stashed links
yln pop

# Watch linked packages for changes (re-links on dep changes, reloads on branch switch)
yln watch
```

## State tracking

`yln` tracks which packages you directly requested in `node_modules/.yln-state.json`.
This allows watch mode to correctly clean up transitive dependencies when a package
drops a workspace dep or when a workspace directory is deleted. The state file is
automatically created on `yln add` / TUI selection and removed by `yln clean`.

## Watch mode

Watch mode monitors the monorepo for changes and re-links automatically. It also
watches `.git/HEAD` in the monorepo — when you switch branches, it reloads workspace
data and re-links, picking up new packages or removing ones that no longer exist on
the new branch.

## Why not `yarn link`?

Yarn's built-in `link` command has ergonomic and correctness issues that make it
impractical for everyday monorepo development.

### Yarn 1: two-step process, no transitive deps

Yarn 1 requires registering a package globally first, then linking it in the consumer.
It only links the one package you ask for — transitive workspace dependencies are not
followed:

```sh
# 1. Register (from the package directory)
cd my-monorepo/packages/pkg-a
yarn link

# 2. Link (from the app directory)
cd my-app
yarn link some-pkg
```

If `pkg-a` depends on another workspace package `pkg-b`, the app keeps its own npm
version of `pkg-b`. This means two different copies of `pkg-b` coexist at runtime —
the app imports the real one, while `pkg-a` internally resolves the monorepo's version
via its own `node_modules`. This leads to subtle bugs with shared state, `instanceof`
checks, and duplicate code.

### Yarn 4: all or nothing

Yarn 4 uses `portal:` resolutions and a single command from the consumer side:

```sh
cd my-app

# Link all workspaces from the target project
yarn link ../my-monorepo/packages/pkg-a --all

# Or list specific packages
yarn link ../my-monorepo/packages/pkg-a ../my-monorepo/packages/pkg-b
```

The `--all` flag links **every workspace sibling** whose name matches any dependency
in the consumer — not just transitive deps of the package you asked for. This silently
replaces unrelated direct dependencies of your app.

Without `--all`, you must manually trace the entire transitive workspace dependency
tree and list every package. If you miss one, resolution fails.

There is no middle ground: either over-link everything or manually track the full
dependency graph.

Yarn 4's `link` also modifies `package.json` and `yarn.lock`, which you may not want.

### Yarn 4: `portal:` vs `link:` protocols

Yarn 4 uses the `portal:` protocol for `yarn link`. There's also a `link:` protocol
with different behavior:

- **`portal:`** — reads the target's `package.json` and resolves its actual
  dependencies in the consumer's context.
- **`link:`** — creates a bare link with no dependencies (empty dep map, version
  `0.0.0`). Much simpler, no transitive dependency resolution.

Portal resolutions are **not persisted to the lockfile** (`shouldPersistResolution()`
returns `false`), so they are re-resolved on every `yarn install`.

### What yln does differently

`yln` replaces `node_modules/` entries with symlinks directly — no `package.json`
modifications, no lockfile changes. It automatically resolves transitive `workspace:*`
dependencies so you get exactly the right set of packages linked, nothing more. The
interactive TUI (`yln edit`) lets you toggle packages and see your current selections,
and watch mode keeps everything in sync as you develop.
