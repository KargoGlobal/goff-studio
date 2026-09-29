# Azure Blob Storage backend

Writes flags straight to blobs in an Azure Storage container, the same layout
GO Feature Flag's `azureBlobStorage` retriever reads. It is a separate Go module
so the Azure SDK only ends up in builds that use it.

## Using it

```sh
go build -o goff-studio-azblob ./cmd/goff-studio-azblob
```

```yaml
storage:
  backend: azblob
  bucket: flags        # the container
  prefix: studio       # optional, invisible to the UI
  options:
    accountURL: https://myaccount.blob.core.windows.net
```

Or with no config file at all:

```sh
GOFF_STUDIO_STORAGE=azblob \
GOFF_STUDIO_STORAGE_BUCKET=flags \
GOFF_STUDIO_STORAGE_OPTIONS='{accountURL: "https://myaccount.blob.core.windows.net"}' \
  ./goff-studio-azblob
```

## Credentials

With `accountURL`, Studio uses `DefaultAzureCredential`. That covers managed
identity and Workload Identity in production and `az login` locally, so no
secret goes in the config. The identity needs **Storage Blob Data
Contributor** on the container.

If `AZURE_STORAGE_CONNECTION_STRING` is set, it takes precedence. A connection
string carries the account key, so Studio only reads it from the environment,
never from the config file. It is mainly for the Azurite emulator.

Each API call has a 30 second timeout.

## What you give up

| | github | azblob |
|---|---|---|
| History | commits | blob versions, only if blob versioning is on |
| Attribution | commit author and trailers | blob metadata, only if blob versioning is on |
| Review | pull requests and CODEOWNERS | none |

With Azure Blob, **Studio's permission config is the only control** over who
can change a flag. Studio warns about this at startup.

Each write records `studio_user_name`, `studio_user_email`, `studio_user_id`
and `studio_message` as blob metadata. Azure requires metadata names to be
valid C# identifiers, so they use underscores. Azure sends metadata as HTTP
headers, so values must be ASCII. Printable ASCII is stored as-is, and anything
else is stored as an RFC 2047 encoded word and decoded on read, the same scheme
the S3 backend uses. Values are truncated to fit the 8 KB metadata limit.

The data API cannot read an account's versioning setting, so Studio infers it.
At startup it lists the container and checks whether blobs carry version IDs.
After that, the first upload that returns a version ID turns history on. An
empty container therefore shows no history panel until the first write.

## Concurrency

The file version is the blob's ETag. Writes use `If-Match`, so a stale write
gets a 412 rather than overwriting the newer blob. If the other change was to a
different flag in the same file, Studio re-applies its change to the fresh blob
and retries. If it was to the same flag, the user gets a conflict to review.
`CreateFile` uses `If-None-Match: *`, so it cannot overwrite an existing blob.

## Verification

The unit tests run against a fake of the `API` interface; nothing in the test
suite talks to Azure. Checked by hand on 2026-09-29 through the real Azure SDK
against [Azurite](https://github.com/Azure/Azurite): create and no-overwrite,
nested listing, not-found, a conditional write, and the service rejecting a
stale ETag. Azurite has no blob versioning, so history is covered by the unit
tests only. Not yet checked against real Azure.
