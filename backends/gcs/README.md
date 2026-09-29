# Google Cloud Storage backend

Writes flags straight to a GCS object, the same layout GO Feature Flag's `gcs`
retriever reads. It is a separate Go module so Google's auth libraries only end
up in builds that use them.

## Using it

```sh
go build -o goff-studio-gcs ./cmd/goff-studio-gcs
```

```yaml
storage:
  backend: gcs
  bucket: my-flags-bucket
  prefix: flags        # optional, invisible to the UI
  options:
    endpoint: http://localhost:4443   # optional, for an emulator
```

Or with no config file at all:

```sh
GOFF_STUDIO_STORAGE=gcs GOFF_STUDIO_STORAGE_BUCKET=my-flags-bucket ./goff-studio-gcs
```

Credentials come from Google Application Default Credentials, so Workload
Identity works with no extra configuration. Studio needs `storage.objects.get`,
`storage.objects.list` and `storage.objects.create` on the bucket (for example
`roles/storage.objectUser`). `Check` also reads the bucket's versioning setting,
and it tolerates being refused. When `options.endpoint` is set, requests are
sent **without** credentials; that is meant for local emulators only.

## What you give up

| | github | gcs |
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

## Live testing

The unit tests run against an `httptest` fake. `live_gcs_test.go` sits behind
the `live` build tag, so a plain `go test ./...` never runs it. It creates,
lists, reads and writes objects, checks that the server rejects a stale
generation, and reads back attribution when the bucket has versioning. Each run
writes under its own `run-<timestamp>` prefix.

Against real GCS, with credentials from Application Default Credentials:

```sh
LIVE_GCS_BUCKET=your-bucket LIVE_GCS_PREFIX=goff-studio-test \
  go test -tags live -run TestLive -v ./...
```

Against [fake-gcs-server](https://github.com/fsouza/fake-gcs-server), an
independent emulator (versioning needs `-backend memory`):

```sh
fake-gcs-server -scheme http -port 4443 -backend memory &
curl -X POST localhost:4443/storage/v1/b -H 'Content-Type: application/json' \
  -d '{"name":"live-bucket","versioning":{"enabled":true}}'
LIVE_GCS_BUCKET=live-bucket LIVE_GCS_ENDPOINT=http://localhost:4443 \
  go test -tags live -run TestLive -v ./...
```

Verified against fake-gcs-server on 2026-09-29 with versioning on and off. It
has not yet been run against real GCS.
