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
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/pcanilho/crossplane-function-resources-merger/input/v1beta1"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

// Parse decodes every string value that is a YAML mapping. It is ParseWith with
// no key restriction, kept for the Input-level parseEmbedded.
func Parse(in map[string]any) map[string]any {
	out, _ := ParseWith(in, nil)
	return out
}

// ParseWith decodes string values that are YAML mappings, restricted to keys
// when it is non-empty. A value that is not a mapping is left untouched.
// The second return names each value's original serialization, detected by its
// first non-whitespace byte. Absent means encode as YAML.
func ParseWith(in map[string]any, keys []string) (map[string]any, map[string]v1beta1.ParseFormat) {
	var only map[string]bool
	if len(keys) > 0 {
		only = make(map[string]bool, len(keys))
		for _, k := range keys {
			only[k] = true
		}
	}
	out := make(map[string]any, len(in))
	origin := map[string]v1beta1.ParseFormat{}
	for k, v := range in {
		if only != nil && !only[k] {
			out[k] = v
			continue
		}
		parsed := parseValue(v)
		out[k] = parsed
		raw, wasString := v.(string)
		if _, stillString := parsed.(string); wasString && !stillString {
			// The "[" branch never fires: parseValue only ever decodes into
			// map[string]any, so a top-level array stays an opaque string.
			if t := strings.TrimLeft(raw, " \t\r\n"); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
				origin[k] = v1beta1.ParseFormatJSON
			} else {
				origin[k] = v1beta1.ParseFormatYAML
			}
		}
	}
	return out, origin
}

// parseValue decodes v into a YAML mapping. A string that is not a mapping
// decodes into a map with an error and is left untouched, so scalars such as
// "no" and "3.10" survive verbatim. A string containing more than one YAML
// document with real content is also left untouched: decoding only the first
// document would silently discard the rest, so the whole string is kept
// opaque and merges as a scalar instead of losing data. A trailing document
// that is empty, comment-only, or an explicit null decodes to a nil value and
// carries no content, so it does not count as a second document: it cannot be
// told apart from a genuinely empty document, and either way there is nothing
// to lose by dropping it.
func parseValue(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	dec := yaml.NewDecoder(strings.NewReader(s))
	decoded := make(map[string]any)
	if err := dec.Decode(&decoded); err != nil {
		return v
	}
	if hasFurtherContent(dec) {
		// A later document decoded to a non-nil value: this string holds
		// multiple YAML documents, so leave it untouched.
		return v
	}
	return decoded
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

// LowerToStringMap lowers merged data for a map[string]string target field,
// encoding every non-string value as YAML.
func LowerToStringMap(in map[string]any) (map[string]any, error) {
	out, _, err := LowerToStringMapWith(in, nil, false)
	return out, err
}

// LowerToStringMapWith lowers merged data, encoding each key in the format
// named for it. A key with no entry, or ParseFormatAuto with no detected JSON
// origin, encodes as YAML, which is the historical behaviour.
//
// When stringify is true, a non-string scalar is coerced to a string instead
// of failing. coerced names every key coerced this way, sorted so the caller's
// message is deterministic.
func LowerToStringMapWith(in map[string]any, format map[string]v1beta1.ParseFormat, stringify bool) (out map[string]any, coerced []string, err error) {
	out = make(map[string]any, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok {
			out[k] = s
			continue
		}
		if isNonStringScalar(v) {
			if !stringify {
				return nil, nil, errors.Errorf("key %q has non-string scalar value of type %T; a ConfigMap requires string values", k, v)
			}
			s, err := scalarToString(v)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "cannot stringify key %q", k)
			}
			out[k] = s
			coerced = append(coerced, k)
			continue
		}
		if format[k] == v1beta1.ParseFormatJSON {
			b, err := json.Marshal(v)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "cannot encode key %q as JSON", k)
			}
			out[k] = string(b)
			continue
		}
		encoded, err := encodeYAML(v)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "cannot encode value of key %q", k)
		}
		out[k] = encoded
	}
	sort.Strings(coerced)
	return out, coerced, nil
}

// scalarToString renders a scalar for a map[string]string target field.
// Trailing zeros are already lost: structpb carries every JSON number as a
// float64, so 3.10 arrives as 3.1. FormatFloat with -1 precision is the best
// available.
func scalarToString(v any) (string, error) {
	switch t := v.(type) {
	case bool:
		return strconv.FormatBool(t), nil
	case string:
		return t, nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(t), nil
	case nil:
		return "", errors.New("value is null; a ConfigMap cannot hold a null")
	default:
		return "", errors.Errorf("unsupported scalar type %T", v)
	}
}

// isNonStringScalar reports whether v is neither a string, a map, nor a list.
// Such a value is an error rather than a guess, because encoding it would not
// round trip.
func isNonStringScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

// encodeYAML encodes v as YAML, 2-space indented.
func encodeYAML(v any) (string, error) {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	_ = enc.Close()
	return buf.String(), nil
}
