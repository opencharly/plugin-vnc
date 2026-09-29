# AGENTS.md — plugin-vnc

Standalone plugin repo for the `vnc` RFB/VNC check verb (`verb:vnc`). The plugin
is a Go module at `candy/plugin-vnc/` (module path
`github.com/opencharly/plugin-vnc/candy/plugin-vnc`); the root `charly.yml` only
declares `discover: candy` so the repo is a project and its candy is scanned.

Canonical files:

- `candy/plugin-vnc/charly.yml` — the `plugin-vnc:` candy entity (`plugin:`
  block, `plan:` checks).
- `candy/plugin-vnc/methods.go` — the `status`/`screenshot`/`click`/`mouse`/
  `type`/`key`/`rfb`/`session` methods.
- `candy/plugin-vnc/vnc_client.go` — the stdlib-only RFB client.
- `candy/plugin-vnc/recorder.go` / `session_method.go` — the detached framebuffer
  recorder.
- `candy/plugin-vnc/schema/vnc.cue` — the self-contained `#VncInput`.
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.
- `README.md` — user overview only; never agent guidance.

## Load these skills first (R0)

- `/charly-internals:plugin` — the plugin authoring reference: the `plugin:`
  block, the unified Provider model, the out-of-process shape, the per-plugin
  CUE-schema contract, placement. Load before touching the provider or schema.
- `/charly-check:vnc` — the `vnc:` verb this candy serves.
- `/charly-check:check` — the check orchestrator, beds and the R10 sequence.
- `/charly-internals:git-workflow` — before any git/PR action.

## Build / validate / test

- `go build ./...` in `candy/plugin-vnc/` — compile the plugin module.
- `go test ./...` in `candy/plugin-vnc/` — the plugin's Go tests
  (`vnc_client_test.go`, `rfb_server_test.go`, `session_test.go`,
  `session_wire_test.go`).
- `charly box validate` at the repo root — the structural check (the candy +
  `plugin:` block, CUE schema).
- The merge gate is the **org-wide** `charly/pr-validator` (required check
  `validate / validate`, defined in `opencharly/.github`); this repo has **no**
  per-repo candy gate.
- R10 consumer: a `wayvnc`-bearing pod bed whose check composes this plugin.

## Modify this repo

- Edit the `plugin-vnc:` candy entity, the Go source, and `schema/vnc.cue`
  **together** — the schema is the single source for the `params/` struct, so a
  field change not mirrored in the schema desyncs the generated types.
- The host owns the endpoint resolution (`cc.ResolveGraphicsEndpoint`); the
  plugin needs no container inspection — keep it that way.

## Landing

- PR-only. Every change lands through a pull request; the org-required
  `charly/pr-validator` validates the diff and body and arms native auto-merge on
  PASS. Direct pushes to `main` are blocked.
- History lives in `CHANGELOG/` (written by `tag-on-merge` at merge time); the PR
  body IS the changelog.
- The authoritative rulebook is the umbrella `AGENTS.md` in
  `opencharly/opencharly` and `charly/AGENTS.md` in the charly repo. Do not
  restate its rules here.
