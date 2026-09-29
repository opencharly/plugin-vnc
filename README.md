# plugin-vnc

VNC/RFB desktop automation for OpenCharly — the `vnc:` check verb.

The verb drives a live deployment's VNC desktop over the RFB protocol (RFC 6143)
with a custom, stdlib-only VNC client (VeNCrypt/TLS + ZRLE decode). It is served
**out-of-process** — charly's loader fetches this repo, host-builds the provider
binary, and serves it over go-plugin gRPC, so the RFB client lives here, out of
charly's core check surface.

The plugin resolves the deployment's VNC endpoint via the generic
`cc.ResolveGraphicsEndpoint` reverse-leg (the host owns the podman / venue /
libvirt / port-mapping machinery) — a container's published port 5900, or a VM's
libvirt-discovered `<graphics type='vnc'>` listener bridged/tunneled to a
host-reachable TCP address — plus the resolved password. It needs no container
inspection at all.

`session` is the host-side detached RFB framebuffer recorder over the runner's
generic background-session service.

## What it provides

| Capability | Surface |
|---|---|
| `verb:vnc` | the `vnc:` check verb — `status`, `screenshot`, `click`, `mouse`, `type`, `key`, `rfb`, `session` |

## How to use it

Compose the plugin candy in a VNC-bearing bed:

```yaml
- '@github.com/opencharly/plugin-vnc/candy/plugin-vnc:<tag>'
```

Then author the verb in a plan:

```yaml
- check: the VNC server is up
  id: vnc-status
  vnc: status
  stdout: [{contains: ok}]
  eventually: 60s
  retry_interval: 5s
  context: [runtime]
```

The R10 consumer is a `wayvnc`-bearing pod bed whose check composes this plugin
(e.g. `sway-browser-vnc` via `sway-desktop-vnc`).

## Layout

- `candy/plugin-vnc/` — the plugin module: `methods.go` (the method surface),
  `vnc_client.go` (the RFB client), `provider.go` / `plugin.go`, `recorder.go` /
  `session_method.go` (the detached recorder), `schema/vnc.cue` (the
  self-contained `#VncInput`), `params/cue_types_gen.go`, `cmd/serve/main.go`.
- `charly.yml` — the root project manifest (`discover: candy`).
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.

## Related

- Owning skill: `/charly-check:vnc` — the `vnc:` RFB/VNC check verb, served
  out-of-process by this candy.
- `/charly-internals:plugin` — the out-of-process plugin model.
- [`opencharly/charly`](https://github.com/opencharly/charly) — the charly CLI.
