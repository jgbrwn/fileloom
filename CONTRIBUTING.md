# Contributing to Fileloom

Fileloom intentionally keeps the filesystem as the content database and plain HTML/CSS as the source format.

Before opening a change:

```bash
make fmt
go test ./...
go vet ./...
```

Please keep generated `site/public` output out of the project Git history, preserve the `/_cms` route shape, and avoid editing vendored VvvebJs files unless an integration change requires it. New CMS mutations should use atomic writes, validate paths, preserve source formatting where possible, and add regression tests.
