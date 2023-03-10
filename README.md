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

Silence the logger:

```sh
gtx -r https://github.com/thewhodidthis/gtx.git -q
```

Only process select branches (in order of appearance):

```sh
gtx -r https://github.com/thewhodidthis/gtx.git -b main -b develop
```

Export a copy of the default HTML page template and quit:

```sh
gtx -r https://github.com/thewhodidthis/gtx.git -e
```

Templates can reference external files in the target directory. These are left intact across script runs making it easier to theme the output by linking in stylesheets and other assets as required. Use the `-t` flag to specify a custom template:

```sh
gtx -r https://github.com/thewhodidthis/gtx.git -t page.html.tmpl
```

## requirements

- `git(1)`

## see also

- [git2html](https://github.com/Hypercubed/git2html)
