---
name: mds
description: Show the user a markdown file rendered in their browser — a plan, a spec, a report, anything too long to paste into the chat. Use when the user asks to open, preview, render or "show me" a markdown file, or right after writing one they will want to read. Do not use for reading a file yourself.
---

# mds

`mds <file.md>` opens the file in the shared mds browser tab and returns immediately. It does
not block, so run it like any other command.

- Do not repeat the URL unless the browser did not open or the user asks for it. Do not paste
  the file contents into the chat.
- The page live-reloads on every save. After editing the file, do not restart mds.
- YFM notes, cuts, tabs and multiline tables are detected automatically; the page has an
  md/yfm button when a particular rendering is needed.
- `mds <dir>` serves every markdown file under a directory, with a file tree in the sidebar.
- For another file, run mds again with its path: it joins the sidebar of the same page,
  and an open mds tab moves straight to it.
- `mds --stop` shuts the server down.

If `mds` is not installed, say so instead of guessing at another renderer:
`go install github.com/melkikh/mds@latest`.
