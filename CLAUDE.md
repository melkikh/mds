# mds — notes for whoever works on this next

A local markdown viewer: one Go binary, assets embedded, no build step, no runtime deps
beyond goldmark/chroma/fsnotify. It renders files off the user's disk in their browser.

## Working here

- `make test` (gofmt check, vet, `go test -race`) before calling anything done. `make check`
  additionally cross-builds for darwin/linux/windows/freebsd.
- `make run ARGS="."` rebuilds, stops the old server and serves this repo. Look at
  the thing you changed; curl it if you cannot click it.
- Client-side changes cannot be proven by Go tests. `node --check` the assets at minimum, and
  for anything stateful (focus, storage, reveal) drive the logic through a throwaway
  simulator rather than reasoning about it in your head. Say what you actually verified.
- Every behaviour change gets a test. Failure messages state the consequence, not the
  expectation: "so an agent reading stderr to the end would block", not "want 0".
- Commit only when asked. Message: `better`. No co-author trailer.

## Layout

| file | holds |
| --- | --- |
| `main.go` | flags, port, detach-by-default, the URL that gets printed |
| `server.go` | routes, auth, roots and their prefixes, cache |
| `render.go` | goldmark setup, frontmatter, directory scan, tree building |
| `watch.go` | fsnotify loop and the SSE stream that reloads open pages |
| `instance.go` | the running-server registry under `os.UserCacheDir` |
| `agent.go` | agent detection, detaching, `--skill` |
| `assets/` | `page.html`, `gate.html`, `app.js`, `app.css` — hand-written, `go:embed`ed |

## Style

- Go: small functions, short names, no ceremony. A comment earns its place by saying *why*;
  what the code does is the code's job. Write them as sentences, not labels.
- No new dependencies without a reason that survives being said out loud. No JS framework, no
  bundler, no CSS preprocessor — `app.js` is a classic script, `app.css` is plain CSS with
  variables.
- Browser state: `data-*` attributes on `<html>` drive CSS, `localStorage.mds*` is
  browser-wide, `sessionStorage` is per-tab. Inline `<script>` needs the CSP nonce; no inline
  event handlers.
- Anything the user reads — help text, gate page, tooltips — is lowercase, plain, and short.

## Invariants worth not breaking

These are load-bearing; there are tests on all of them, and a test failing here is a real
finding, not a fixture to update.

- Non-loopback `Host` is refused before anything else, auth included.
- Three secrets, deliberately separate: `token` (master, in the fragment and the instances
  file, mds itself only, alone may mount paths), `session` (the cookie — cookies are not
  scoped by port, so treat it as read-only), `action` (in the page markup, useless without
  the cookie). Never put `token` in the DOM or in the cookie.
- Everything is behind `/_auth`; `/_auth` itself is the only unauthenticated route.
- Markdown renders with `WithUnsafe`, so the CSP is the only thing between a hostile file and
  the page: nonce for scripts, off-host images only under `MDS_REMOTE_IMAGES`.
- Every file read goes through `os.OpenRoot`. Only `.md`/`.markdown` and image extensions are
  served, ever.
- Every root has a prefix, including the first; `resolve` returns `nil` for an unknown one. A
  prefix once handed out is never given to a different directory.

## Docs

The usage string in `main.go`, `README.md`, and `assets/skill.md` describe the same behaviour
to different audiences. When shared behaviour changes, update all three; do not copy detail
that only one audience needs into the other two.

`mds --help` is a command reference, not a manual. It has a hard limit of 24 lines and 88
columns, enforced by `TestUsageFitsOneScreen`: synopsis, flags, env vars, and at most two
short lines about the runtime model. No feature tour or security explanation.

`README.md` is for a person deciding whether to use this, not a changelog and not a feature
list. One screen, hard limit. It says what mds is, how to install it, how to run it, the
flags and env vars, and how to hand it to an agent — nothing else. The key gets one short
line because it is needed to open the page; the CSP, sidebar, private directories and
frontmatter folding stay in the page itself or out of user-facing docs. A new feature is
usually a few words inside an existing sentence; a feature that seems to need its own
section does not get one. When it grows, cut it back.
