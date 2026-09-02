# Cluster scoped example

A cluster scoped composite (`XMerger`) merges two `ConfigMap`s and two `EnvironmentConfig`s into a
single `ConfigMap` in the `ephemeral` namespace.

What it exercises:

- `mergeStrategy: ForceMergeObjects`, so later sources win. `map-2` overrides `database.port` and
  `owner`, `envcfg-2` overrides nothing because it is nested.
- `parseEmbedded: true`, so the `app.yaml` blob is parsed and deep merged rather than replaced.
- `toFieldPath: overrides` on `envcfg-2`, placing its contribution under a subtree.
- `nameFromCompositeFieldPath: spec.appName`, so the target is named from the composite.
- `target.context.key`, publishing the merged result to the Composition context as well.

| File | |
|---|---|
| `xrd.yaml` | the composite type, `scope: Cluster` |
| `xr.yaml` | one composite, `spec.appName: merged` |
| `composition.yaml` | the pipeline step and the function's `Merge` input |
| `required-resources.yaml` | the four sources, as `render` cannot fetch them itself |
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

The output should match `rendered.golden.yaml`. Redirect it over that file to regenerate the
golden; do not hand edit it, since the owner reference `uid` is derived from the composite's
identity.
