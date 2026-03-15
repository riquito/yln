# yln — yarn linker

A fast CLI tool to link monorepo workspace packages into standalone apps via
symlinks, resolving transitive workspace dependencies automatically.

## Problem

Yarn 4's `yarn link --all` over-links (grabs every workspace sibling). Without
`--all`, you must manually trace every transitive workspace dependency. `yln`
solves this by recursively resolving only the workspace deps actually needed.

## How it works

Given a monorepo with workspaces and an app with `node_modules/`, `yln`:

1. Reads the monorepo's workspace packages
2. For each requested package, recursively walks its `workspace:*` dependencies
3. Replaces matching entries in the app's `node_modules/` with symlinks to the
   monorepo packages
No changes to `package.json`, `yarn.lock`, or metadata files. Just symlinks.
Linked packages are detected by scanning `node_modules/` for symlinks.

## CLI

### `yln [packages...]`

Link packages from a monorepo into the current app.

```sh
# Interactive: launch TUI fuzzy picker (requires monorepo to be configured)
yln

# Link specific packages (recursively resolves their workspace deps)
yln lodash-es

# Link multiple starting points
yln lodash-es ramda

# Specify monorepo path (overrides config)
yln lodash-es --monorepo ~/projects/my-monorepo
```

Running `yln` with no arguments launches an interactive TUI with fuzzy search
over the monorepo's workspace packages. Requires a monorepo to be configured
(via `--monorepo` or config file).

### `yln list`

Show currently linked packages and where they point.

```sh
yln list
```

### `yln clean`

Remove all links, restoring original `node_modules/` state.

```sh
yln clean
```

This is all-or-nothing by design. Selectively unlinking individual packages
while leaving others linked is unreliable — transitive dependencies may be
shared between linked packages, so removing one could break another.

## Configuration

Config file: `~/.config/yln/config.toml`

```toml
# Default monorepo path (used when --monorepo is not specified)
monorepo = "~/projects/my-monorepo"
```

The `--monorepo` flag always takes precedence over the config file.

## Link detection

No metadata file needed. `yln list` and `yln clean` work by scanning
`node_modules/` for symlinks. The symlink target path tells you where each
package points. This approach:

- Can't go stale — it reflects the actual filesystem state
- Works even if someone manually added/removed symlinks
- Keeps the implementation simple

For `yln clean`, remove all symlinks in `node_modules/` and run `yarn install`
to restore the real packages.

## Linking algorithm

```
function link(packageName, monorepo, appNodeModules):
    pkg = monorepo.findWorkspace(packageName)
    if pkg is null:
        return  // not a workspace package, skip

    if packageName already linked:
        return  // avoid cycles

    remove appNodeModules/packageName if exists
    symlink appNodeModules/packageName -> pkg.path

    for dep in pkg.workspaceDependencies:
        link(dep.name, monorepo, appNodeModules)  // recurse
```

Only `workspace:*` (and `workspace:^`, `workspace:~`) dependencies are
followed. Regular npm dependencies are left alone — they resolve from the
monorepo's own `node_modules` via Node's symlink resolution, same as the
manual symlink approach.

## Tech stack

- Go
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) for the TUI
  (fuzzy picker)
- No yarn/node dependency at runtime — reads `package.json` files directly

## Milestones

### v0.1 — Core linking

- [ ] Read monorepo workspace structure from `package.json` workspaces field
- [ ] Resolve workspace packages by expanding glob patterns
- [ ] Parse `workspace:*` dependencies from each package
- [ ] Recursive symlink creation with cycle detection
- [ ] Symlink scanning for link detection
- [ ] `yln <packages...>` CLI with `--monorepo` flag
- [ ] `yln list`
- [ ] `yln clean`
- [ ] Config file support (`~/.config/yln/config.toml`)

### v0.2 — Interactive TUI

- [x] Fuzzy search picker over monorepo workspaces
- [x] Multi-select support
- [x] Launch on bare `yln` invocation

### v0.3 — Polish

- [ ] Pre-link validation (check app has `node_modules/`, packages exist, etc.)
- [ ] Colored output and progress feedback
- [ ] `--dry-run` flag
- [ ] Handle edge cases (nested `node_modules/`, scoped packages)

### Future ideas

- Watch mode: re-link on workspace changes
- Support for pnpm/npm workspaces (not just yarn)
- Shell completions
