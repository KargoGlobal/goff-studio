# Google Cloud Storage backend

Writes flags straight to a GCS object, the same layout GO Feature Flag's
`googleStorage` retriever reads. Built into the standard `goff-studio` binary and image; leave it out with `-tags no_googlestorage`.
The old kind name `gcs` still works, with a warning.

## Using it

```yaml
storage:
  kind: googleStorage
  bucket: my-flags-bucket
  prefix: flags        # optional, invisible to the UI
  options:
    endpoint: http://localhost:4443   # optional, for an emulator
```

Or with no config file at all:

```sh
GOFF_STUDIO_STORAGE=googleStorage GOFF_STUDIO_STORAGE_BUCKET=my-flags-bucket ./goff-studio
```

Credentials come from Google Application Default Credentials, so Workload
Identity works with no extra configuration. Studio needs `storage.objects.get`,
`storage.objects.list` and `storage.objects.create` on the bucket (for example
`roles/storage.objectUser`). `Check` also reads the bucket's versioning setting,
and it tolerates being refused. When `options.endpoint` is set, requests are
sent **without** credentials; that is meant for local emulators only.

## What you give up

| | github | googleStorage |
|---|---|---|
| History | commits | object generations, only if object versioning is on |
| Attribution | commit author and trailers | custom metadata, only if object versioning is on |
| Review | pull requests and CODEOWNERS | none |

With GCS, **Studio's permission config is the only control** over who can
change a flag. Studio warns about this at startup.

Each write records `studio-user-name`, `studio-user-email`, `studio-user-id`
and `studio-message` as custom metadata. The JSON API accepts UTF-8 values, so
unlike S3 nothing is encoded. The values are truncated to fit the 8 KiB custom
metadata limit. A versioned listing returns every generation's metadata, so the
history panel needs one list call rather than one call per version.

## Concurrency

The file version is the object's generation. Writes are multipart uploads with
`ifGenerationMatch` set to the generation that was read, so a stale write gets
a 412 rather than overwriting the newer object. If the other change was to a
different flag in the same file, Studio re-applies its change to the fresh
object and retries. If it was to the same flag, the user gets a conflict to
review. `CreateFile` uses `ifGenerationMatch=0`, so it cannot overwrite an
existing object.

## Why the JSON API and not the Cloud client library

Studio makes five calls: read, list, conditional upload, versioned list and
bucket get. The client library would bring in gRPC and a large dependency tree
to make them. Plain HTTP with `golang.org/x/oauth2/google` keeps the binary
small, and the tests can run against an `httptest` fake of the API.

## Verification

The unit tests run against an `httptest` fake of the GCS JSON API; nothing in
the test suite talks to Google Cloud. Checked by hand on 2026-09-29 against
[fake-gcs-server](https://github.com/fsouza/fake-gcs-server), an independent
emulator, with object versioning both on and off: create and no-overwrite,
listing, reads, a conditional write, the server rejecting a stale generation,
and history carrying attribution. Not yet checked against real GCS.
