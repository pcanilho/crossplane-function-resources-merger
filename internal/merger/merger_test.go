package merger

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/pcanilho/crossplane-function-resources-merger/input/v1alpha2"
)

const keepVal = "keep"

func TestMerge(t *testing.T) {
	cases := map[string]struct {
		strategy v1alpha2.MergeStrategy
		dst      map[string]any
		src      map[string]any
		want     map[string]any
	}{
		"ForceMergeObjectsIncomingWins": {
			strategy: v1alpha2.StrategyForceMergeObjects,
			dst:      map[string]any{"a": "1", "b": keepVal},
			src:      map[string]any{"a": "2"},
			want:     map[string]any{"a": "2", "b": keepVal},
		},
		"MergeObjectsExistingWins": {
			strategy: v1alpha2.StrategyMergeObjects,
			dst:      map[string]any{"a": "1"},
			src:      map[string]any{"a": "2", "c": "3"},
			want:     map[string]any{"a": "1", "c": "3"},
		},
		"ForceMergeObjectsIsDeep": {
			strategy: v1alpha2.StrategyForceMergeObjects,
			dst:      map[string]any{"n": map[string]any{"x": "1", "y": keepVal}},
			src:      map[string]any{"n": map[string]any{"x": "2"}},
			want:     map[string]any{"n": map[string]any{"x": "2", "y": keepVal}},
		},
		"ReplaceSwapsSubtree": {
			strategy: v1alpha2.StrategyReplace,
			dst:      map[string]any{"n": map[string]any{"x": "1", "y": "gone"}},
			src:      map[string]any{"n": map[string]any{"x": "2"}},
			want:     map[string]any{"n": map[string]any{"x": "2"}},
		},
		"AppendArrays": {
			strategy: v1alpha2.StrategyForceMergeObjectsAppendArrays,
			dst:      map[string]any{"l": []any{"a"}},
			src:      map[string]any{"l": []any{"b"}},
			want:     map[string]any{"l": []any{"a", "b"}},
		},
		"DefaultEmptyStrategyIsForceMerge": {
			strategy: "",
			dst:      map[string]any{"a": "1"},
			src:      map[string]any{"a": "2"},
			want:     map[string]any{"a": "2"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dstBefore := deepCopyMap(tc.dst)
			srcBefore := deepCopyMap(tc.src)

			got, err := Merge(tc.dst, tc.src, tc.strategy)
			if err != nil {
				t.Fatalf("Merge(...): unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Merge(...): -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(dstBefore, tc.dst); diff != "" {
				t.Errorf("Merge(...): dst was modified, -before, +after:\n%s", diff)
			}
			if diff := cmp.Diff(srcBefore, tc.src); diff != "" {
				t.Errorf("Merge(...): src was modified, -before, +after:\n%s", diff)
			}
		})
	}
}

func TestMergeUnknownStrategy(t *testing.T) {
	if _, err := Merge(map[string]any{}, map[string]any{}, "Nonsense"); err == nil {
		t.Error("Merge(...): want error for unknown strategy, got nil")
	}
}

// TestMergeNormalizesNonStringKeyedMaps covers transformer.Parse's output for
// a YAML blob with non-string keys: gopkg.in/yaml.v3 decodes such a mapping as
// map[any]any even where the enclosing map is map[string]any. Left unhandled,
// that type is not JSON encodable and fails later with an unrelated-looking
// "cannot set target field" error.
func TestMergeNormalizesNonStringKeyedMaps(t *testing.T) {
	src := map[string]any{
		"n": map[any]any{1: "a", 2: "b"},
	}

	got, err := Merge(map[string]any{}, src, v1alpha2.StrategyForceMergeObjects)
	if err != nil {
		t.Fatalf("Merge(...): unexpected error: %v", err)
	}

	want := map[string]any{
		"n": map[string]any{"1": "a", "2": "b"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Merge(...): -want, +got:\n%s", diff)
	}
}

// TestMergeDropsExplicitNullKeyUnderSomeStrategies documents the mergo hazard
// that Normalize exists to avoid: merging src onto an empty dst is not a
// no-op. MergeObjects and MergeObjectsAppendArrays, the two strategies
// without mergo.WithOverride, silently drop a top-level key whose value is an
// explicit nil, even though that key does not exist in dst at all.
func TestMergeDropsExplicitNullKeyUnderSomeStrategies(t *testing.T) {
	cases := map[string]struct {
		strategy v1alpha2.MergeStrategy
		survives bool
	}{
		"ForceMergeObjects":             {v1alpha2.StrategyForceMergeObjects, true},
		"MergeObjects":                  {v1alpha2.StrategyMergeObjects, false},
		"ForceMergeObjectsAppendArrays": {v1alpha2.StrategyForceMergeObjectsAppendArrays, true},
		"MergeObjectsAppendArrays":      {v1alpha2.StrategyMergeObjectsAppendArrays, false},
		"Replace":                       {v1alpha2.StrategyReplace, true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			src := map[string]any{"nullKey": nil, "other": "x"}

			got, err := Merge(map[string]any{}, src, tc.strategy)
			if err != nil {
				t.Fatalf("Merge(...): unexpected error: %v", err)
			}

			_, ok := got["nullKey"]
			if ok != tc.survives {
				t.Errorf("Merge(...) under %q: nullKey present = %v, want %v", tc.strategy, ok, tc.survives)
			}
		})
	}
}

// TestNormalizePreservesExplicitNullKeys covers the mechanism mergeSources
// uses to seed its accumulator from a lone or first source: a top-level
// explicit-nil key must survive. Normalize takes no strategy and applies no
// merge, which is exactly why it does not hit the mergo hazard demonstrated in
// TestMergeDropsExplicitNullKeyUnderSomeStrategies above.
func TestNormalizePreservesExplicitNullKeys(t *testing.T) {
	src := map[string]any{"nullKey": nil, "other": "x"}

	got := Normalize(src)

	v, ok := got["nullKey"]
	if !ok {
		t.Fatal("Normalize(...) dropped the explicit nil key")
	}
	if v != nil {
		t.Errorf("Normalize(...): nullKey = %v, want nil", v)
	}
}

// TestMergeResultDoesNotAliasInputs mutates the map returned by Merge and
// checks that dst and src are unaffected, catching a result that aliases
// input structure even when nothing wrote through it.
func TestMergeResultDoesNotAliasInputs(t *testing.T) {
	t.Run("Replace", func(t *testing.T) {
		dst := map[string]any{
			keepVal: map[string]any{"z": "kept"},
			"n":     map[string]any{"x": "1", "y": "gone"},
		}
		src := map[string]any{
			"n": map[string]any{"x": "2"},
		}

		got, err := Merge(dst, src, v1alpha2.StrategyReplace)
		if err != nil {
			t.Fatalf("Merge(...): unexpected error: %v", err)
		}

		got["n"].(map[string]any)["x"] = "mutated"
		got[keepVal].(map[string]any)["z"] = "mutated"

		if v := src["n"].(map[string]any)["x"]; v != "2" {
			t.Errorf("mutating the result mutated src[\"n\"][\"x\"]: got %q, want %q", v, "2")
		}
		if v := dst["n"].(map[string]any)["x"]; v != "1" {
			t.Errorf("mutating the result mutated dst[\"n\"][\"x\"]: got %q, want %q", v, "1")
		}
		if v := dst[keepVal].(map[string]any)["z"]; v != "kept" {
			t.Errorf("mutating the result mutated dst[\"keep\"][\"z\"]: got %q, want %q", v, "kept")
		}
	})

	t.Run("MergoPath", func(t *testing.T) {
		dst := map[string]any{
			"n": map[string]any{"x": "1", "y": keepVal},
		}
		src := map[string]any{
			"n":     map[string]any{"x": "2"},
			"extra": map[string]any{"e": "only-src"},
		}

		got, err := Merge(dst, src, v1alpha2.StrategyForceMergeObjects)
		if err != nil {
			t.Fatalf("Merge(...): unexpected error: %v", err)
		}

		got["n"].(map[string]any)["x"] = "mutated"
		got["extra"].(map[string]any)["e"] = "mutated"

		if v := dst["n"].(map[string]any)["x"]; v != "1" {
			t.Errorf("mutating the result mutated dst[\"n\"][\"x\"]: got %q, want %q", v, "1")
		}
		if v := dst["n"].(map[string]any)["y"]; v != keepVal {
			t.Errorf("mutating the result mutated dst[\"n\"][\"y\"]: got %q, want %q", v, keepVal)
		}
		if v := src["n"].(map[string]any)["x"]; v != "2" {
			t.Errorf("mutating the result mutated src[\"n\"][\"x\"]: got %q, want %q", v, "2")
		}
		if v := src["extra"].(map[string]any)["e"]; v != "only-src" {
			t.Errorf("mutating the result mutated src[\"extra\"][\"e\"]: got %q, want %q", v, "only-src")
		}
	})
}
