// Package merger compiles a merge strategy and applies it.
package merger

import (
	"fmt"

	"dario.cat/mergo"
	"github.com/pcanilho/crossplane-function-resources-merger/input/v1alpha2"
	"github.com/pkg/errors"
)

// Options returns the mergo options for a strategy. Replace has no option
// representation; it is handled by Merge before mergo is reached.
func Options(s v1alpha2.MergeStrategy) ([]func(*mergo.Config), error) {
	switch s {
	case "", v1alpha2.StrategyForceMergeObjects:
		return []func(*mergo.Config){mergo.WithOverride}, nil
	case v1alpha2.StrategyMergeObjects:
		return nil, nil
	case v1alpha2.StrategyForceMergeObjectsAppendArrays:
		return []func(*mergo.Config){mergo.WithOverride, mergo.WithAppendSlice}, nil
	case v1alpha2.StrategyMergeObjectsAppendArrays:
		return []func(*mergo.Config){mergo.WithAppendSlice}, nil
	case v1alpha2.StrategyReplace:
		return nil, nil
	default:
		return nil, errors.Errorf("unknown merge strategy %q", s)
	}
}

// Merge applies src onto dst under the given strategy and returns the result.
// Neither dst nor src is modified, and the returned map shares no nested map
// or slice with either input.
func Merge(dst, src map[string]any, s v1alpha2.MergeStrategy) (map[string]any, error) {
	if s == v1alpha2.StrategyReplace {
		out := deepCopyMap(dst)
		for k, v := range src {
			out[k] = deepCopyValue(v)
		}
		return out, nil
	}

	opts, err := Options(s)
	if err != nil {
		return nil, err
	}

	out := deepCopyMap(dst)
	if err := mergo.Merge(&out, deepCopyMap(src), opts...); err != nil {
		return nil, errors.Wrap(err, "cannot merge")
	}
	return out, nil
}

// Normalize deep copies m, recursing into map[string]any, map[any]any and
// []any exactly as Merge does, but applies no merge strategy. Use it to seed
// an accumulator from a single source's data rather than routing it through
// Merge: mergo silently drops a top-level key whose value is an explicit nil
// under MergeObjects and MergeObjectsAppendArrays, even though the key does
// not exist in the destination, which is the wrong outcome for data that
// isn't being merged with anything yet.
func Normalize(m map[string]any) map[string]any {
	return deepCopyMap(m)
}

// deepCopyValue copies v, recursing into map[string]any, map[any]any and
// []any so the copy shares no nested structure with v. transformer.Parse
// produces map[any]any for a blob with non-string keys; it is normalized to
// map[string]any here so the result stays JSON-encodable like every other
// parsed value.
func deepCopyValue(v any) any {
	switch vv := v.(type) {
	case map[string]any:
		return deepCopyMap(vv)
	case map[any]any:
		return deepCopyAnyMap(vv)
	case []any:
		return deepCopySlice(vv)
	default:
		return v
	}
}

// deepCopyAnyMap copies m into a map[string]any, coercing each key to a
// string.
func deepCopyAnyMap(m map[any]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[fmt.Sprint(k)] = deepCopyValue(v)
	}
	return out
}

// deepCopyMap returns a copy of m whose values share no nested map[string]any
// or []any structure with m.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

// deepCopySlice returns a copy of s whose elements share no nested
// map[string]any or []any structure with s.
func deepCopySlice(s []any) []any {
	if s == nil {
		return nil
	}
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = deepCopyValue(v)
	}
	return out
}
