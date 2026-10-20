package devin

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"testing"
)

func collect[T any](seq iter.Seq2[T, error]) ([]T, []error) {
	var items []T
	var errs []error
	for item, err := range seq {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		items = append(items, item)
	}
	return items, errs
}

func cursor(value string) *string { return &value }

func TestIterateMultiplePages(t *testing.T) {
	ctx := context.Background()
	var requests []ListOptions
	pages := map[string]*Page[int]{
		"":       {Items: []int{1, 2}, HasNextPage: true, EndCursor: cursor("page-1")},
		"page-1": {Items: []int{3}, HasNextPage: true, EndCursor: cursor("page-2")},
		"page-2": {Items: []int{4}},
	}
	items, errs := collect(Iterate(ctx, func(_ context.Context, opts ListOptions) (*Page[int], error) {
		requests = append(requests, opts)
		return pages[opts.After], nil
	}, ListOptions{First: 2}))

	if !reflect.DeepEqual(items, []int{1, 2, 3, 4}) || len(errs) != 0 {
		t.Fatalf("got items %v and errors %v", items, errs)
	}
	want := []ListOptions{{First: 2}, {First: 2, After: "page-1"}, {First: 2, After: "page-2"}}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestIteratePreservesInitialCursor(t *testing.T) {
	var requests []ListOptions
	items, errs := collect(Iterate(context.Background(), func(_ context.Context, opts ListOptions) (*Page[int], error) {
		requests = append(requests, opts)
		return &Page[int]{Items: []int{len(requests)}, HasNextPage: len(requests) == 1, EndCursor: cursor("next")}, nil
	}, ListOptions{First: 7, After: "starting"}))
	if !reflect.DeepEqual(items, []int{1, 2}) || len(errs) != 0 {
		t.Fatalf("got items %v and errors %v", items, errs)
	}
	want := []ListOptions{{First: 7, After: "starting"}, {First: 7, After: "next"}}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestIterateEmptyPagesWithNextCursor(t *testing.T) {
	var requests int
	items, errs := collect(Iterate(context.Background(), func(_ context.Context, _ ListOptions) (*Page[int], error) {
		requests++
		if requests == 1 {
			return &Page[int]{HasNextPage: true, EndCursor: cursor("next")}, nil
		}
		return &Page[int]{Items: []int{9}}, nil
	}, ListOptions{}))
	if !reflect.DeepEqual(items, []int{9}) || len(errs) != 0 || requests != 2 {
		t.Fatalf("got items %v, errors %v, requests %d", items, errs, requests)
	}
}

func TestIterateRejectsRepeatedAndCyclicCursors(t *testing.T) {
	for name, cursors := range map[string][]string{
		"immediate repeat": {"a", "a"},
		"cycle":            {"a", "b", "a"},
	} {
		t.Run(name, func(t *testing.T) {
			requests := 0
			_, errs := collect(Iterate(context.Background(), func(_ context.Context, _ ListOptions) (*Page[int], error) {
				cursorValue := cursors[requests]
				requests++
				return &Page[int]{HasNextPage: true, EndCursor: cursor(cursorValue)}, nil
			}, ListOptions{}))
			if len(errs) != 1 || !errors.Is(errs[0], errRepeatedCursor) {
				t.Fatalf("errors = %v, want repeated-cursor error", errs)
			}
			if requests != len(cursors) {
				t.Fatalf("requests = %d, want %d", requests, len(cursors))
			}
		})
	}
}

func TestIterateRejectsMissingCursorAndNilPage(t *testing.T) {
	for name, fetch := range map[string]func(context.Context, ListOptions) (*Page[int], error){
		"missing cursor": func(context.Context, ListOptions) (*Page[int], error) {
			return &Page[int]{HasNextPage: true}, nil
		},
		"nil page": func(context.Context, ListOptions) (*Page[int], error) {
			return nil, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := collect(Iterate(context.Background(), fetch, ListOptions{}))
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want one error", errs)
			}
			if name == "missing cursor" && !errors.Is(errs[0], errMissingCursor) {
				t.Fatalf("error = %v, want missing-cursor error", errs[0])
			}
			if name == "nil page" && !errors.Is(errs[0], errNilPage) {
				t.Fatalf("error = %v, want nil-page error", errs[0])
			}
		})
	}
}

func TestIterateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, errs := collect(Iterate(ctx, func(context.Context, ListOptions) (*Page[int], error) {
		called = true
		return &Page[int]{}, nil
	}, ListOptions{}))
	if called || len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
		t.Fatalf("called = %v, errors = %v", called, errs)
	}
}

func TestIterateNilContext(t *testing.T) {
	called := false
	_, errs := collect(Iterate[int](nil, func(context.Context, ListOptions) (*Page[int], error) {
		called = true
		return &Page[int]{Items: []int{1}}, nil
	}, ListOptions{}))
	if called || len(errs) != 1 || !errors.Is(errs[0], errNilContext) {
		t.Fatalf("called = %v, errors = %v", called, errs)
	}
}

func TestIterateFetchError(t *testing.T) {
	wantErr := errors.New("fetch failed")
	_, errs := collect(Iterate(context.Background(), func(context.Context, ListOptions) (*Page[int], error) {
		return nil, wantErr
	}, ListOptions{}))
	if len(errs) != 1 || !errors.Is(errs[0], wantErr) {
		t.Fatalf("errors = %v, want fetch error", errs)
	}
}

func TestIterateStopsAfterEarlyBreak(t *testing.T) {
	requests := 0
	for item, err := range Iterate(context.Background(), func(_ context.Context, _ ListOptions) (*Page[int], error) {
		requests++
		return &Page[int]{Items: []int{1, 2}, HasNextPage: true, EndCursor: cursor("next")}, nil
	}, ListOptions{}) {
		if err != nil || item != 1 {
			t.Fatalf("got item %d and error %v", item, err)
		}
		break
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestIterateStateIsIndependent(t *testing.T) {
	requests := 0
	fetch := func(_ context.Context, opts ListOptions) (*Page[int], error) {
		requests++
		return &Page[int]{Items: []int{requests}, HasNextPage: opts.After == "", EndCursor: cursor("next")}, nil
	}
	seq := Iterate(context.Background(), fetch, ListOptions{})
	first, firstErrs := collect(seq)
	second, secondErrs := collect(seq)
	if !reflect.DeepEqual(first, []int{1, 2}) || !reflect.DeepEqual(second, []int{3, 4}) || len(firstErrs) != 0 || len(secondErrs) != 0 {
		t.Fatalf("first = %v (%v), second = %v (%v)", first, firstErrs, second, secondErrs)
	}
}
