package transformer

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const appYAMLKey = "app.yaml"

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
