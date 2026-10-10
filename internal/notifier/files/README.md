# casamod-store-notifier

Installed automatically by the **CasaMOD Store** container. It reads
`status.json` (written by the container into this folder, served by CasaOS at
`/mod/casamod-store-notifier/status.json`) and, when updates are waiting,
shows a small notice on the CasaOS desktop with a link to the store.

When the store's own updates are set to *Update automatically*, it also
asks CasaOS to update the CasaMOD Store app (the call behind "Check then
update", with the logged-in session), once per new image.

Do not edit these files: the container replaces them when it is updated.
Turn the notice off in the store's Settings instead.
