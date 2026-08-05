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
mds docs -d 2
```

Prints the URL and opens a browser. Edit the file, the page reloads itself.

Run mds again with another path and it does not start a second server: the path joins the
sidebar of the page that is already open, and the printed URL points straight at it.

```
-d, --depth N      how deep to walk, 0 is root only, -1 unlimited (default 5)
-s, --skip NAMES   dirs to ignore (default node_modules,vendor,dist,build,target)
-b, --background   serve detached, print the URL and exit
    --new          start a separate server instead of joining the running one
    --no-open      do not open a browser, just print the URL
    --stop         stop every running server
    --skill        print a skill file that teaches a coding agent to use mds
-h, --help
```

Started by a coding agent (Claude Code, Codex, ...) mds detaches on its own, so the agent
does not sit there waiting for a server that never exits.

To have an agent reach for mds without being told, put the skill file wherever that agent
keeps its instructions — every one of them has a different place and format, so redirect it
yourself rather than guess:

```
mkdir -p ~/.claude/skills/mds && mds --skill > ~/.claude/skills/mds/SKILL.md
mds --skill >> ~/.codex/AGENTS.md
```

Dot-directories are always skipped.

## In the page

Code blocks have a copy button. The pencil opens the file in your editor, set `MDS_EDITOR="code -g"` to pick one.

GFM (tables, task lists, strikethrough), syntax highlighting and mermaid diagrams — nothing to configure.
