package downloader

import (
	"slices"
	"testing"
)

func TestSearchDict_EmptyDict(t *testing.T) {
	got := slices.Collect(SearchDict(map[string]any{}, "test"))
	if len(got) != 0 {
		t.Fatalf("expected no values, got %v", got)
	}
}

func TestSearchDict_SimpleDict(t *testing.T) {
	got := slices.Collect(SearchDict(map[string]any{"test": "expected"}, "test"))
	if len(got) != 1 || got[0] != "expected" {
		t.Fatalf("expected [expected], got %v", got)
	}
}

func TestSearchDict_DictInsideList(t *testing.T) {
	in := []any{map[string]any{"test": "expected"}}
	got := slices.Collect(SearchDict(in, "test"))
	if len(got) != 1 || got[0] != "expected" {
		t.Fatalf("expected [expected], got %v", got)
	}
}

func TestSearchDict_DuplicateKeysInList(t *testing.T) {
	in := []any{
		map[string]any{"test": "expected"},
		map[string]any{"test": "expected"},
	}
	got := slices.Collect(SearchDict(in, "test"))
	if len(got) != 2 {
		t.Fatalf("expected 2 values, got %v", got)
	}
}

func TestSearchDict_NestedDicts(t *testing.T) {
	in := map[string]any{
		"a": map[string]any{"test": "expected"},
		"b": map[string]any{"test": "expected"},
	}
	got := slices.Collect(SearchDict(in, "test"))
	if len(got) != 2 {
		t.Fatalf("expected 2 values, got %v", got)
	}
}

func TestSearchDict_FirstShortCircuits(t *testing.T) {
	in := map[string]any{
		"outer": map[string]any{
			"inner": map[string]any{"test": "first"},
			"more":  map[string]any{"test": "second"},
		},
	}
	v, ok := SearchDictFirst(in, "test")
	if !ok {
		t.Fatal("expected a match")
	}
	if v != "first" && v != "second" {
		t.Fatalf("expected first/second, got %v", v)
	}
}

func BenchmarkSearchDict(b *testing.B) {
	test := map[string]any{}
	for i := 1; i < 30; i++ {
		list := make([]any, 10)
		for j := range list {
			list[j] = j
		}
		test[itoa(i)] = list
	}
	b.ResetTimer()
	for b.Loop() {
		_ = slices.Collect(SearchDict(test, "test"))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	buf := [20]byte{}
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}
