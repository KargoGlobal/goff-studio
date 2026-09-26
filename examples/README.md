# Example flags repository

A tiny flags repository with one running experiment and the seed metric
catalog, for trying the Experiments area locally:

```yaml
# studio.yaml
storage:
  backend: file
  path: ./examples
discoverEnvironments: true
analysis:
  provider: sample
```

- `production/checkout.goff.yaml` holds the `checkout` flag the experiment runs on.
- `experiments/checkout-exp-us-east-1.yaml` is the registry entry.
- `metrics/*.yaml` is the metric catalog.

`provider: sample` makes Studio generate demo results, labelled as such in the
UI. Without it, the results page says no analysis provider is configured.
