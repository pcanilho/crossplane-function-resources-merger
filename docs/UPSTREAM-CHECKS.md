# Upstream behaviour checks

These pin assumptions about Crossplane, not about this function. Everything this
function itself does is covered by `go test ./...`.

Run them when `XP_CORE_VERSION` in `.github/workflows/ci.yaml` changes, not on
every release. `XP_VERSION` pins the Crossplane CLI and has no bearing on these
checks. Record the version and date in the table at the bottom.

`crossplane render` cannot substitute for these. Its in-memory client hardcodes
`IsObjectNamespaced` to true and returns a nil RESTMapper, so it never runs the
scope logic under test.

## Checks

Against a cluster with the target Crossplane, using a namespaced XRD:

1. A composed ConfigMap with no namespace is created in the XR's namespace.
2. A composed ConfigMap asking for a different namespace is created in the XR's
   namespace anyway, with a `NamespaceOverridden` warning event.
3. A composed cluster-scoped EnvironmentConfig with no namespace fails with
   `cannot apply cluster scoped composed resource ... for a namespaced composite
   resource.` and creates nothing.
4. The same but with a namespace set creates the object, wedges the XR, and
   leaves an object the GC reports as `OwnerRefInvalidNamespace`. This is the
   behaviour Decision 1 exists to avoid; if it ever stops happening, Decision 1
   can be revisited.
5. A namespaced XR can still read a Secret from another namespace via required
   resources. If this ever fails, Crossplane has added read confinement and this
   function's own guard may be redundant.

## Verified against

| Crossplane | date | result |
|---|---|---|
| v2.3.4 | 2026-08-29 | all five as described, run against a live cluster |
| v2.4.0 | 2026-08-29 | carried over from v2.3.4 by source diff, see below |

A live run is the real check. When only the pin moves, diffing the three files
these checks depend on is enough to carry the previous result forward:

- `internal/controller/apiextensions/composite/composition_render.go`, which
  force sets the namespace. Identical in v2.3.4 and v2.4.0.
- `internal/xfn/required_resources.go`, which fetches required resources. No
  change on the fetch path.
- `internal/controller/apiextensions/composite/composition_functions.go`, which
  holds the `IsObjectNamespaced` gate. Its only v2.4.0 change touching namespace
  handling is a new dependency tracker that records composed resource references
  for watches. It runs after the composition result is built and does not affect
  the gate.

If any of those three change in a way that touches the gate, the force set or
the fetch, run the five checks against a live cluster instead.
