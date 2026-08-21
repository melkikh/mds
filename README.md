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
mds                     the current directory
mds docs -d 2
```

Prints a URL, opens a browser and exits — the server stays behind, the terminal stays yours.
Edit the file and the page reloads itself. `mds --stop` when you are done. Run mds again with
another path and it joins the page already open, and `mds --service install` puts an empty
server in your login items so there is always one to join. The URL carries a one-off key, so
pass it around whole.

```
-d, --depth N      how deep to walk, 0 is root only, -1 unlimited (default 5)
-s, --skip NAMES   dirs to ignore (default node_modules,vendor,dist,build,target)
-f, --foreground   keep the server in this terminal instead of detaching
    --new          start a separate server instead of joining the running one
    --no-open      do not open a browser, just print the URL
    --service ACT  the server that starts at login: install, remove, status, restart, run
    --stop         stop every running server
    --skill        print a skill file that teaches a coding agent to use mds
-h, --help

MDS_PORT           port number, e.g. 9000 (default 6337)
MDS_EDITOR         what the pencil button opens, e.g. "code -g"
MDS_REMOTE_IMAGES  1 lets a document load images from other hosts, 0 blocks them (default 0)
```

## For coding agents

`mds --skill` prints a skill file that teaches an agent to show you a rendered file instead
of pasting it into the chat. Every agent keeps its instructions somewhere else, so redirect
it yourself:

```
mkdir -p ~/.claude/skills/mds && mds --skill > ~/.claude/skills/mds/SKILL.md
mds --skill >> ~/.codex/AGENTS.md
```
