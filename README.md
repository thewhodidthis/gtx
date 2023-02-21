## about

Go based [`git2html`](https://github.com/Hypercubed/git2html) remake with custom templating support to help when using `git(1)` as an archival tool basically.

## setup

Download from GitHub directly:

```sh
go install github.com/thewhodidthis/gtx
```

## usage

Calling without any arguments prints out the default settings. At the very least pass it a repo to be parsing through:

```sh
# NOTE: Will save output in the current directory.
gtx -r https://github.com/thewhodidthis/gtx.git
```

Export a copy of the default HTML page template and quit:

```sh
gtx -r https://github.com/thewhodidthis/gtx.git -e
```

Use a custom page template:

```sh
gtx -r https://github.com/thewhodidthis/gtx.git -t page.html.tmpl
```

Silence the logger:

```sh
gtx -r https://github.com/thewhodidthis/gtx.git -q
```

## requirements

- `git(1)`

## see also

- [git2html](https://github.com/Hypercubed/git2html)
