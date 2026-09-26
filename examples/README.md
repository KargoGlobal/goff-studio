# Example flags repository

A tiny flags repository with one running experiment and the seed metric
catalog, for trying the Experiments area locally:

```yaml
# studio.yaml
storage:
  backend: file
  path: ./examples
discoverEnvironments: true
```

- `production/checkout.goff.yaml` holds the `checkout` flag the experiment runs on.
- `experiments/checkout-exp-us-east-1.yaml` is the registry entry.
- `metrics/*.yaml` is the metric catalog.

With no `analysis.baseURL` configured, Studio shows generated sample results,
labelled as such in the UI.
