package devin

import (
	"context"
	"errors"
	"fmt"
	"iter"
)

var (
	errNilPage        = errors.New("pagination fetch returned a nil page")
	errMissingCursor  = errors.New("pagination page has a next page but no end cursor")
	errRepeatedCursor = errors.New("pagination cursor did not advance")
	errNilFetcher     = errors.New("pagination fetch function is nil")
	errNilContext     = errors.New("pagination context is nil")
)

// Page is one page of results returned by a paginated API.
type Page[T any] struct {
	Items       []T     `json:"items"`
	HasNextPage bool    `json:"has_next_page"`
	EndCursor   *string `json:"end_cursor"`
	Total       *int    `json:"total,omitempty"`
}

// ListOptions controls the size and starting cursor of a paginated request.
type ListOptions struct {
	First int
	After string
}

// Iterate lazily fetches and yields every item from a paginated list.
//
// The options are copied for each iteration, with After updated to the
// preceding page's end cursor. Fetch errors, cancellation, and invalid cursor
// sequences are yielded once as errors, after which iteration stops. Returning
// false from yield stops iteration immediately without fetching another page.
func Iterate[T any](ctx context.Context, fetch func(context.Context, ListOptions) (*Page[T], error), opts ListOptions) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		if ctx == nil {
			yield(*new(T), errNilContext)
			return
		}
		if fetch == nil {
			yield(*new(T), errNilFetcher)
			return
		}

		request := opts
		seenCursors := make(map[string]struct{})
		if request.After != "" {
			seenCursors[request.After] = struct{}{}
		}

		for {
			if err := ctx.Err(); err != nil {
				yield(*new(T), err)
				return
			}

			page, err := fetch(ctx, request)
			if err != nil {
				yield(*new(T), err)
				return
			}
			if err := ctx.Err(); err != nil {
				yield(*new(T), err)
				return
			}
			if page == nil {
				yield(*new(T), errNilPage)
				return
			}

			for _, item := range page.Items {
				if err := ctx.Err(); err != nil {
					yield(*new(T), err)
					return
				}
				if !yield(item, nil) {
					return
				}
			}

			if !page.HasNextPage {
				return
			}
			if page.EndCursor == nil || *page.EndCursor == "" {
				yield(*new(T), errMissingCursor)
				return
			}
			if _, exists := seenCursors[*page.EndCursor]; exists {
				yield(*new(T), fmt.Errorf("%w: %q", errRepeatedCursor, *page.EndCursor))
				return
			}

			request = opts
			request.After = *page.EndCursor
			seenCursors[request.After] = struct{}{}
		}
	}
}
