# S3 storage backend

Writes flags straight to an S3 object. Built into the standard `goff-studio` binary and image; leave it out with `-tags no_s3`.

## Using it

```yaml
storage:
  kind: s3
  bucket: my-flags-bucket
  region: us-east-1
  prefix: flags        # optional, invisible to the UI
  options:
    endpoint: http://localhost:9000   # optional, for MinIO
```

Or with no config file at all:

```sh
docker run -e GOFF_STUDIO_STORAGE=s3 \
           -e GOFF_STUDIO_STORAGE_BUCKET=my-flags-bucket \
           -e GOFF_STUDIO_STORAGE_REGION=us-east-1 ...
```

Credentials come from the default AWS chain, so IRSA works with no extra
configuration.

## What you give up

| | github | s3 |
|---|---|---|
| History | commits | object versions, only if bucket versioning is on |
| Attribution | commit author and trailers | none |
| Review | pull requests and CODEOWNERS | none |

With S3, **Studio's permission config is the only control** over who can change
a flag. There is no second gate. Studio warns about this at startup.

History appears only when the bucket has versioning enabled — Studio checks at
boot and reports the capability honestly, so the UI hides the panel rather than
showing an empty one.

## Concurrency

Writes use `PutObject` with `If-Match` on the object's ETag, so a stale write
gets a 412 instead of silently clobbering. A change to a different flag in the
same file is re-applied to the fresh object and retried; a change to the same
flag returns a conflict for the user to review. `CreateFile` uses
`If-None-Match: *` so it cannot overwrite an existing object.

## Why not GO Feature Flag's own s3 retriever

It returns `[]byte` and discards the download metadata, so there is no ETag to
build concurrency on, no object listing to discover environments, and it reads
one fixed key. Fine for an SDK that only reads one file; not enough for an
editor. This backend uses the AWS SDK directly.

## Verification

The unit tests run against a fake S3 API; nothing in the test suite talks to
AWS. Checked by hand against real S3 on 2026-09-23: reads return a bare ETag,
listing discovers environments, a write changes the ETag, a stale version
conflicts rather than clobbering, S3 rejected a mismatched `If-Match` with 412,
and `CreateFile` refused to overwrite. GO Feature Flag's own `s3retrieverv2`
then read the file Studio had written and evaluated the flag with no error.
