# Building and publishing plugins

The root `Makefile` builds every plugin in `PLUGINS`, including `qqbot_note`:

```sh
make build
```

For distributable Linux packages for all configured plugins:

```sh
make package
```

This creates per-plugin, per-platform archives in `dist/` and `dist/manifests.tar.gz`.
The package archives contain the plugin manifest, configuration schema, icon, and
platform binary in the layout expected under the Media Agent `server/plugins/`
directory.

The GitHub Actions workflow uploads these files as workflow artifacts for normal
builds. To publish them as GitHub Release assets, push a version tag matching
`v*` (for example, `v1.2.3`) to a commit on the repository. The tag-triggered run
builds a complete plugin snapshot, creates or updates the matching GitHub Release,
and uploads all plugin archives, `manifests.tar.gz`, and `checksums.txt`.

Running the workflow manually with `workflow_dispatch` builds and uploads workflow
artifacts only; it does not create a GitHub Release.
