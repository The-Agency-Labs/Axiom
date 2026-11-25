package providers

// Metadata is a provider's metadata for one title, with exactly the keys
// and value shapes the Django app's provider functions return (see
// src/app/providers/*.py), so API responses stay identical. Numbers are
// float64 or int as encoding/json would produce them; build values with
// plain Go types (string, int, float64, bool, nil, []any, map[string]any,
// []Metadata).
type Metadata = map[string]any

// SearchPage is a provider's search response, shaped like the Django app's.
type SearchPage = map[string]any

// Str returns m[key] as a string ("" when missing or not a string).
func Str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// Int returns m[key] as an int (0 when missing or not a number).
func Int(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// List returns m[key] as a list of objects (nil when missing).
func List(m map[string]any, key string) []map[string]any {
	switch v := m[key].(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, e := range v {
			if obj, ok := e.(map[string]any); ok {
				out = append(out, obj)
			}
		}
		return out
	}
	return nil
}
