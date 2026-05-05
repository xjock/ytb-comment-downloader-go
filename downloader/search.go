package downloader

import "iter"

// SearchDict recursively walks a JSON-decoded structure (map[string]any /
// []any / scalars) and yields every value whose key equals searchKey.
//
// It mirrors the Python `search_dict` generator: depth-first, stack-based,
// lazy — callers can break early to stop the walk.
func SearchDict(partial any, searchKey string) iter.Seq[any] {
	return func(yield func(any) bool) {
		stack := []any{partial}
		for len(stack) > 0 {
			n := len(stack) - 1
			current := stack[n]
			stack = stack[:n]

			switch v := current.(type) {
			case map[string]any:
				for key, val := range v {
					if key == searchKey {
						if !yield(val) {
							return
						}
						continue
					}
					stack = append(stack, val)
				}
			case []any:
				for _, item := range v {
					stack = append(stack, item)
				}
			}
		}
	}
}

// SearchDictFirst returns the first value found for searchKey, or zero value
// and false if none exists. It short-circuits the iteration.
func SearchDictFirst(partial any, searchKey string) (any, bool) {
	for v := range SearchDict(partial, searchKey) {
		return v, true
	}
	return nil, false
}
