// Package transformer parses embedded config blobs and lowers merged data back
// to the target's field type.
//
// Parse and LowerToStringMap preserve values but not key order, indentation,
// comments, or anchors: decoding a blob into map[string]any loses YAML key
// order, so a blob that round-trips through this package comes back
// alphabetically sorted and 2-space indented even when nothing merged into it.
// A blob holding more than one YAML document with real content is left
// unparsed to avoid discarding the extra documents; a document that only
// trails off with an empty or comment-only separator carries no content and
// does not trigger this, so the blob is still parsed and deep merged.
package transformer

import (
	"strings"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

// Parse decodes string values that are YAML mappings so their contents can be
// deep merged. A string that is not a mapping decodes into a map with an error
// and is left untouched, so scalars such as "no" and "3.10" survive verbatim.
// A string containing more than one YAML document with real content is also
// left untouched: decoding only the first document would silently discard the
// rest, so the whole string is kept opaque and merges as a scalar instead of
// losing data. A trailing document that is empty, comment-only, or an
// explicit null decodes to a nil value and carries no content, so it does not
// count as a second document: it cannot be told apart from a genuinely empty
// document, and either way there is nothing to lose by dropping it.
func Parse(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok {
			dec := yaml.NewDecoder(strings.NewReader(s))
			decoded := make(map[string]any)
			if err := dec.Decode(&decoded); err == nil {
				if hasFurtherContent(dec) {
					// A later document decoded to a non-nil value: this
					// string holds multiple YAML documents, so leave it
					// untouched.
					out[k] = v
					continue
				}
				out[k] = decoded
				continue
			}
		}
		out[k] = v
	}
	return out
}

// hasFurtherContent reports whether dec has at least one more YAML document
// carrying a non-nil value. It drains trailing documents that decode to nil,
// which covers a bare "---" with nothing after it, comment-only or
// whitespace-only trailing documents, and an explicit "null" document.
func hasFurtherContent(dec *yaml.Decoder) bool {
	for {
		var extra any
		if err := dec.Decode(&extra); err != nil {
			return false
		}
		if extra != nil {
			return true
		}
	}
}

// LowerToStringMap converts merged data for a target field typed
// map[string]string, such as a ConfigMap's data. Strings pass through
// unencoded. Maps and lists are YAML encoded. A non-string scalar is an error
// rather than a guess, because encoding it would not round trip.
func LowerToStringMap(in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case string:
			out[k] = t
		case map[string]any, []any:
			var buf strings.Builder
			enc := yaml.NewEncoder(&buf)
			enc.SetIndent(2)
			if err := enc.Encode(t); err != nil {
				return nil, errors.Wrapf(err, "cannot encode value of key %q", k)
			}
			_ = enc.Close()
			out[k] = buf.String()
		default:
			return nil, errors.Errorf("key %q has non-string scalar value of type %T; a ConfigMap requires string values", k, v)
		}
	}
	return out, nil
}
