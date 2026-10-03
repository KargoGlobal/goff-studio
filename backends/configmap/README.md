# Kubernetes ConfigMap backend

Stores flags in Kubernetes ConfigMaps. Each environment is one ConfigMap and
each team file is one key in it:

```
production/growth.goff.yaml  ->  ConfigMap goff-production, key growth.goff.yaml
```

That is the shape GO Feature Flag's Kubernetes retriever reads (one key of one
ConfigMap), so each relay proxy or SDK points at the ConfigMap and key for its
environment and team.

It talks to the Kubernetes API with the standard library rather than
client-go, so it adds no dependencies. Built into the standard `goff-studio` binary and image; leave it out with `-tags no_configmap`.

## Using it

```yaml
storage:
  kind: configmap
  prefix: goff-          # environment "production" is ConfigMap "goff-production"
  options:
    namespace: flags     # optional; defaults to Studio's own namespace
```

An environment is a ConfigMap whose name starts with the prefix and that holds
at least one `.yaml` or `.yml` key. Unrelated ConfigMaps in the namespace, such
as `kube-root-ca.crt`, are never shown. Creating a team in a new environment
creates its ConfigMap.

## Credentials and RBAC

In a pod, Studio uses its service account: the API server address comes from
`KUBERNETES_SERVICE_HOST`/`KUBERNETES_SERVICE_PORT`, and the CA and token from
the mounted service account files. The token is re-read for each request, so
rotated projected tokens keep working. Each API call has a 30 second timeout.

The service account needs this in the ConfigMaps' namespace:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: goff-studio
  namespace: flags
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get", "list", "create", "update"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: goff-studio
  namespace: flags
subjects:
  - kind: ServiceAccount
    name: goff-studio
    namespace: studio
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: goff-studio
```

For local development, run `kubectl proxy` and point Studio at it. Requests to
`apiServer` are sent without credentials, since the proxy adds your own:

```yaml
storage:
  kind: configmap
  prefix: goff-
  options:
    apiServer: http://127.0.0.1:8001
    namespace: flags
```

## What you give up

| | github | configmap |
|---|---|---|
| History | commits | none |
| Attribution | commit author and trailers | none |
| Review | pull requests and CODEOWNERS | none |

A ConfigMap keeps no history, so the UI hides the history panel. **Studio's
permission config is the only control** over who can change a flag through
Studio, and anyone with `update` on the ConfigMaps can change them outside
Studio. Studio warns about this at startup.

Kubernetes caps a ConfigMap at 1 MiB, which applies to all of an environment's
team files together. A save that would exceed it fails with the API server's
error.

## Concurrency

The file version is the ConfigMap's `resourceVersion`. A write is a JSON merge
patch that sets only the one key and carries the `resourceVersion` it read, so
if anything changed the ConfigMap in between, the API server answers 409
instead of applying it. Because it is a patch, not a replacement, fields Studio
does not manage are never touched: annotations and labels (which Helm and Argo
CD use to track the ConfigMap), `binaryData`, owner references and finalizers. Every file in an
environment shares one ConfigMap, so a change to another team's file also moves
the version. In that case Studio re-reads, confirms the flag being saved did
not change, re-applies the edit and retries. If the same flag did change, the
user gets a conflict to review. Creating a file never overwrites an existing
key.

## Verification

The unit tests run against an `httptest` fake of the ConfigMap API that
enforces `resourceVersion` conflicts; nothing in the test suite talks to a
cluster. The backend has not yet been run against a real cluster.
