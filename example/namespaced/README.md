# Namespaced example

A namespaced composite (`XNsMerger`) in `team-a` merges three sources into a `ConfigMap` that
Crossplane pins to the composite's own namespace.

What it exercises, all of it specific to a namespaced composite:

- **The target sets no `namespace`.** Crossplane pins it to the composite's. Leaving the field
  empty is what arms Crossplane's own scope check, so never set it here.
- **Cross namespace reads need two opt-ins.** `shared` sets `allowCrossNamespace: true` and
  `platform` appears in `allowedSourceNamespaces`. Either one alone fails the composition, and the
  source is dropped before its selector is ever sent, so the read does not happen.
- **A same namespace source needs neither**, as `map-1` in `team-a` shows.
- **A cluster scoped source is always readable**, as `envcfg-1` shows, and declares no namespace.

| File | |
|---|---|
| `xrd.yaml` | the composite type, `scope: Namespaced` |
| `xr.yaml` | one composite, in `team-a` |
| `composition.yaml` | the pipeline step and the function's `Merge` input |
| `required-resources.yaml` | the three sources, as `render` cannot fetch them itself |
| `functions.yaml` | the function, annotated to run locally |
| `rendered.golden.yaml` | expected output, diffed by CI |

Run it with the function listening on `:9443`:

```shell
go run . --insecure                 # from the repository root, in another shell

crossplane composition render xr.yaml composition.yaml functions.yaml \
  --xrd xrd.yaml \
  --required-resources required-resources.yaml \
  --crossplane-image xpkg.crossplane.io/crossplane/crossplane:v2.4.0
```

> [!IMPORTANT]
> `render` cannot check scope. Its in-memory client hardcodes `IsObjectNamespaced` to true and
> returns no RESTMapper, so it will happily render a cluster scoped target that a real cluster
> rejects. Anything touching namespace or scope has to be validated against a live cluster.
