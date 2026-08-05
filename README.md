# mds

Tiny local server that renders markdown in the browser. Single binary, everything embedded, works offline.

## Install

```
go install github.com/melkikh/mds@latest
```

Needs `$(go env GOPATH)/bin` in your `PATH`.

## Usage

```
mds plan.md             one file
mds                     current dir, md files behind the burger button
mds docs -d 2 -t dark
```

Prints the URL and opens a browser. Edit the file, the page reloads itself.

```
-d, --depth N      how deep to walk, 0 is root only, -1 unlimited (default 5)
-t, --theme NAME   light or dark, there is a toggle in the UI anyway (default light)
-s, --skip NAMES   dirs to ignore (default node_modules,vendor,dist,build,target)
-b, --background   serve detached, print the URL and exit
    --skill        print a skill file that teaches a coding agent to use mds
-h, --help
```

Started by a coding agent (Claude Code, Codex, ...) mds detaches on its own, so the agent
does not sit there waiting for a server that never exits.

Dot-directories are always skipped.

GFM (tables, task lists, strikethrough), syntax highlighting and mermaid diagrams — nothing to configure.
