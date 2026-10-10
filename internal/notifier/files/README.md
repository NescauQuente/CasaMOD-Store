# casamod-store-notifier

Installed automatically by the **CasaMOD Store** container. It reads
`status.json` (written by the container into this folder, served by CasaOS at
`/mod/casamod-store-notifier/status.json`) and, when updates are waiting,
shows a small notice on the CasaOS desktop with a link to the store.

Do not edit these files: the container replaces them when it is updated.
Turn the notice off in the store's Settings instead.
