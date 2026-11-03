# assets

Assets shared by several apps, and asset sources (design files, vector sources, raw audio and video). Final assets used by only one app live inside that app.

## Rules

- Organize by type, such as `icons/`, `images/`, `design/`, `fonts/`.
- Apps use exported assets, copied by the app or referenced at build time. Apps never modify the sources here.
- Before committing a file larger than 10 MB, ask the user. The user decides whether to use Git LFS.
- Record the source and license of every third-party asset in the list below.

## Assets

| asset | source | license |
|---|---|---|
