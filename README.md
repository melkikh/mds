# mds

A tiny local browser viewer for Markdown and [YFM](https://diplodoc.com/docs/en/index-yfm).
One binary, works offline.

## Install

```sh
go install github.com/melkikh/mds@latest
```

## Use

```sh
mds plan.md   # open a file
mds docs      # open a directory
mds           # open the current directory

mds --install # start at login
mds --remove  # remove from login
mds --stop    # stop the server
mds --skill   # print agent instructions
mds --help
```

Each path opens in the same browser tab and reloads on save.

Settings: `MDS_EDITOR`, `MDS_REMOTE_IMAGES`.

For a coding agent, add the output of `mds --skill` to its instructions.
