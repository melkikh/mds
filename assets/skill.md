---
name: mds
description: Show the user a markdown file rendered in their browser — a plan, a spec, a report, anything too long to paste into the chat. Use when the user asks to open, preview, render or "show me" a markdown file, or right after writing one they will want to read. Do not use for reading a file yourself.
---

# mds

`mds <file.md>` starts a local server, prints its URL and returns immediately. It does not
block, so run it like any other command.

- Give the user the URL from stdout whole, `#key=...` and all: it is what unlocks the page.
  Do not also paste the file contents into the chat.
- The page live-reloads on every save. After editing the file, do not restart mds.
- `mds <dir>` serves every markdown file under a directory, with a file tree in the sidebar.
- For another file, run mds again with its path: it joins the sidebar of the same page,
  and the URL that mds prints points straight at it.
- `mds --stop` shuts the server down.

If `mds` is not installed, say so instead of guessing at another renderer:
`go install github.com/melkikh/mds@latest`.
