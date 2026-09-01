package transformer

import (
	"math"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/pcanilho/crossplane-function-resources-merger/input/v1beta1"
)

const (
	appYAMLKey  = "app.yaml"
	jsonKeyName = "config.json"
)

func TestParse(t *testing.T) {
	cases := map[string]struct {
		in   map[string]any
		want map[string]any
	}{
		"YAMLMappingBecomesMap": {
			in:   map[string]any{appYAMLKey: "a: 1\nb: two\n"},
			want: map[string]any{appYAMLKey: map[string]any{"a": 1, "b": "two"}},
		},
		"ScalarStringUntouched": {
			in:   map[string]any{"log_level": "no", "version": "3.10", "port": "8080"},
			want: map[string]any{"log_level": "no", "version": "3.10", "port": "8080"},
		},
		"NonStringUntouched": {
			in:   map[string]any{"n": 1},
			want: map[string]any{"n": 1},
		},
		"MultiDocumentStringLeftUnchanged": {
			in:   map[string]any{appYAMLKey: "a: 1\n---\nb: 2\n"},
			want: map[string]any{appYAMLKey: "a: 1\n---\nb: 2\n"},
		},
		"SingleDocumentStringStillParses": {
			in:   map[string]any{appYAMLKey: "a: 1\nc: 3\n"},
			want: map[string]any{appYAMLKey: map[string]any{"a": 1, "c": 3}},
		},
		"QuotedScalarWithDashesNotDocumentSeparator": {
			in:   map[string]any{appYAMLKey: "key: \"a --- b\"\n"},
			want: map[string]any{appYAMLKey: map[string]any{"key": "a --- b"}},
		},
		"BlockScalarWithDashesNotDocumentSeparator": {
			in:   map[string]any{appYAMLKey: "note: |\n  a\n  ---\n  b\n"},
			want: map[string]any{appYAMLKey: map[string]any{"note": "a\n---\nb\n"}},
		},
		"LeadingSeparatorStillParses": {
			in:   map[string]any{appYAMLKey: "---\na: 1\n"},
			want: map[string]any{appYAMLKey: map[string]any{"a": 1}},
		},
		"LeadingAndTrailingSeparatorStillParses": {
			in:   map[string]any{appYAMLKey: "---\na: 1\n---\n"},
			want: map[string]any{appYAMLKey: map[string]any{"a": 1}},
		},
		"TrailingBareSeparatorStillParses": {
			in:   map[string]any{appYAMLKey: "a: 1\n---\n"},
			want: map[string]any{appYAMLKey: map[string]any{"a": 1}},
		},
		"TrailingSeparatorWithCommentStillParses": {
			in:   map[string]any{appYAMLKey: "a: 1\n---\n# comment\n"},
			want: map[string]any{appYAMLKey: map[string]any{"a": 1}},
		},
		"TrailingSeparatorWithExplicitNullStillParses": {
			in:   map[string]any{appYAMLKey: "a: 1\n---\nnull\n"},
			want: map[string]any{appYAMLKey: map[string]any{"a": 1}},
		},
		"MultipleTrailingBareSeparatorsStillParse": {
			in:   map[string]any{appYAMLKey: "a: 1\n---\n---\n---\n"},
			want: map[string]any{appYAMLKey: map[string]any{"a": 1}},
		},
		"EmptyDocumentThenRealContentLeftUnchanged": {
			in:   map[string]any{appYAMLKey: "a: 1\n---\n---\nb: 2\n"},
			want: map[string]any{appYAMLKey: "a: 1\n---\n---\nb: 2\n"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, Parse(tc.in)); diff != "" {
				t.Errorf("Parse(...): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestLowerToStringMap(t *testing.T) {
	t.Run("MapBecomesYAML", func(t *testing.T) {
		got, err := LowerToStringMap(map[string]any{appYAMLKey: map[string]any{"a": 1}})
		if err != nil {
			t.Fatalf("LowerToStringMap(...): unexpected error: %v", err)
		}
		if got[appYAMLKey] != "a: 1\n" {
			t.Errorf("LowerToStringMap(...): got %q, want %q", got[appYAMLKey], "a: 1\n")
		}
	})

	t.Run("ListBecomesYAMLNotGoFormatting", func(t *testing.T) {
		got, err := LowerToStringMap(map[string]any{"l": []any{"a", "b"}})
		if err != nil {
			t.Fatalf("LowerToStringMap(...): unexpected error: %v", err)
		}
		if got["l"] == "[a b]" {
			t.Error("LowerToStringMap(...): emitted Go formatting instead of YAML")
		}
		if got["l"] != "- a\n- b\n" {
			t.Errorf("LowerToStringMap(...): got %q, want %q", got["l"], "- a\n- b\n")
		}
	})

	t.Run("StringPassesThroughVerbatim", func(t *testing.T) {
		got, err := LowerToStringMap(map[string]any{"v": "3.10"})
		if err != nil {
			t.Fatalf("LowerToStringMap(...): unexpected error: %v", err)
		}
		if got["v"] != "3.10" {
			t.Errorf("LowerToStringMap(...): got %q, want %q", got["v"], "3.10")
		}
	})

	t.Run("NonStringScalarIsAnError", func(t *testing.T) {
		_, err := LowerToStringMap(map[string]any{"n": 1})
		if err == nil {
			t.Fatal("LowerToStringMap(...): want error for non-string scalar, got nil")
		}
		if !strings.Contains(err.Error(), `"n"`) {
			t.Errorf("LowerToStringMap(...): error %q does not name the offending key %q", err.Error(), "n")
		}
	})
}

func TestParseWithScopesToKeys(t *testing.T) {
	in := map[string]any{
		appYAMLKey: "a: 1\n",
		"prose":    "error: connection refused",
	}
	got, _ := ParseWith(in, []string{appYAMLKey})
	if _, ok := got[appYAMLKey].(map[string]any); !ok {
		t.Errorf("%s: want it parsed to a map, got %T", appYAMLKey, got[appYAMLKey])
	}
	if s, ok := got["prose"].(string); !ok || s != "error: connection refused" {
		t.Errorf("prose: want it left as an unparsed string, got %#v", got["prose"])
	}
}

func TestJSONRoundTripsAsJSON(t *testing.T) {
	in := map[string]any{jsonKeyName: `{"name":"svc","port":8080}`}
	parsed, detected := ParseWith(in, nil)
	if detected[jsonKeyName] != v1beta1.ParseFormatJSON {
		t.Errorf("detected origin: got %q, want JSON", detected[jsonKeyName])
	}
	got, _, err := LowerToStringMapWith(parsed, detected, false)
	if err != nil {
		t.Fatalf("LowerToStringMapWith(): unexpected error: %v", err)
	}
	want := `{"name":"svc","port":8080}`
	if got[jsonKeyName] != want {
		t.Errorf("%s: got %v, want %v", jsonKeyName, got[jsonKeyName], want)
	}
}

// TestExplicitFormatOverridesDetectedOrigin covers LowerToStringMapWith's
// format map winning over the origin ParseWith detected: an explicit YAML
// format forces YAML output for a value that arrived as JSON, and vice versa.
func TestExplicitFormatOverridesDetectedOrigin(t *testing.T) {
	t.Run("ExplicitYAMLOverridesDetectedJSON", func(t *testing.T) {
		in := map[string]any{jsonKeyName: `{"a":1}`}
		parsed, detected := ParseWith(in, nil)
		if detected[jsonKeyName] != v1beta1.ParseFormatJSON {
			t.Fatalf("detected origin: got %q, want JSON", detected[jsonKeyName])
		}
		got, _, err := LowerToStringMapWith(parsed, map[string]v1beta1.ParseFormat{jsonKeyName: v1beta1.ParseFormatYAML}, false)
		if err != nil {
			t.Fatalf("LowerToStringMapWith(): unexpected error: %v", err)
		}
		if got[jsonKeyName] != "a: 1\n" {
			t.Errorf("%s: got %q, want YAML encoding", jsonKeyName, got[jsonKeyName])
		}
	})

	t.Run("ExplicitJSONOverridesDetectedYAML", func(t *testing.T) {
		in := map[string]any{appYAMLKey: "a: 1\n"}
		parsed, detected := ParseWith(in, nil)
		if detected[appYAMLKey] != v1beta1.ParseFormatYAML {
			t.Fatalf("detected origin: got %q, want YAML", detected[appYAMLKey])
		}
		got, _, err := LowerToStringMapWith(parsed, map[string]v1beta1.ParseFormat{appYAMLKey: v1beta1.ParseFormatJSON}, false)
		if err != nil {
			t.Fatalf("LowerToStringMapWith(): unexpected error: %v", err)
		}
		if got[appYAMLKey] != `{"a":1}` {
			t.Errorf("%s: got %q, want JSON encoding", appYAMLKey, got[appYAMLKey])
		}
	})
}

// TestLowerToStringMapWithJSONMarshalError covers the JSON encode error path:
// a NaN float has no JSON representation.
func TestLowerToStringMapWithJSONMarshalError(t *testing.T) {
	in := map[string]any{"bad.json": map[string]any{"n": math.NaN()}}
	_, _, err := LowerToStringMapWith(in, map[string]v1beta1.ParseFormat{"bad.json": v1beta1.ParseFormatJSON}, false)
	if err == nil {
		t.Fatal("LowerToStringMapWith(): want error for a NaN value, got nil")
	}
	if !strings.Contains(err.Error(), `"bad.json"`) {
		t.Errorf("LowerToStringMapWith(): error %q does not name the offending key", err.Error())
	}
}

// TestLowerToStringMapWithStringifyCoercesScalars covers the stringify path: a
// non-string scalar is coerced instead of failing, and coerced is returned
// sorted regardless of map iteration order.
func TestLowerToStringMapWithStringifyCoercesScalars(t *testing.T) {
	in := map[string]any{
		"replicas": 3,
		"ratio":    3.1,
		"enabled":  true,
		"name":     "already-a-string",
	}
	out, coerced, err := LowerToStringMapWith(in, nil, true)
	if err != nil {
		t.Fatalf("LowerToStringMapWith(): unexpected error: %v", err)
	}
	if out["replicas"] != "3" || out["ratio"] != "3.1" || out["enabled"] != "true" {
		t.Errorf("coerced values: got %#v", out)
	}
	if out["name"] != "already-a-string" {
		t.Errorf("already-string value must pass through untouched, got %v", out["name"])
	}
	want := []string{"enabled", "ratio", "replicas"}
	if diff := cmp.Diff(want, coerced); diff != "" {
		t.Errorf("coerced keys (-want +got):\n%s", diff)
	}
}

// TestLowerToStringMapWithStringifyStillRejectsNull covers the one scalar
// stringify does not rescue: a null cannot round-trip through a
// map[string]string target, coerced or not.
func TestLowerToStringMapWithStringifyStillRejectsNull(t *testing.T) {
	_, _, err := LowerToStringMapWith(map[string]any{"n": nil}, nil, true)
	if err == nil {
		t.Fatal("LowerToStringMapWith(): want error for a null value even with stringify, got nil")
	}
	if !strings.Contains(err.Error(), `"n"`) {
		t.Errorf("LowerToStringMapWith(): error %q does not name the offending key", err.Error())
	}
}

// TestScalarToString covers every branch scalarToString takes, including the
// two LowerToStringMapWith can never reach on its own: a plain string, which
// is already handled before isNonStringScalar is consulted, and an unsupported
// type, which no JSON or YAML decode ever produces.
func TestScalarToString(t *testing.T) {
	cases := map[string]struct {
		in      any
		want    string
		wantErr bool
	}{
		"Bool":        {in: true, want: "true"},
		"String":      {in: "already", want: "already"},
		"Float64":     {in: 3.5, want: "3.5"},
		"Int":         {in: 7, want: "7"},
		"Null":        {in: nil, wantErr: true},
		"Unsupported": {in: complex(1, 2), wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := scalarToString(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("scalarToString(%#v): want an error, got nil", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("scalarToString(%#v): unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("scalarToString(%#v): got %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDocumentedParseMutations pins the mutations the README promises, so the
// documentation cannot drift from behaviour.
func TestDocumentedParseMutations(t *testing.T) {
	in := map[string]any{
		"config.json": `{"name":"svc","port":8080}`,
		"blob":        "a: no\nb: 3.10\n",
		"prose":       "error: connection refused",
		"plain":       "connection refused",
		"bareno":      "no",
	}
	got, err := LowerToStringMap(Parse(in))
	if err != nil {
		t.Fatalf("LowerToStringMap(): unexpected error: %v", err)
	}
	want := map[string]string{
		"config.json": "name: svc\nport: 8080\n",
		"blob":        "a: \"no\"\nb: 3.1\n",
		"prose":       "error: connection refused\n",
		"plain":       "connection refused",
		"bareno":      "no",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("key %q: got %q, want %q", k, got[k], w)
		}
	}
}
