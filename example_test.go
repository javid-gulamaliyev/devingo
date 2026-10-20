package devin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/url"
	"os"
	"strconv"

	devin "devingo"
)

func ExampleOptional() {
	request := devin.CreateSessionRequest{
		Prompt:    "Review this pull request.",
		Resumable: devin.Ptr(false),
		Title:     devin.Null[string](),
		Tags:      devin.Value([]string{}),
	}
	payload, _ := json.Marshal(request)
	fmt.Println(string(payload))
	// Output: {"prompt":"Review this pull request.","resumable":false,"tags":[],"title":null}
}

// This example is compiled by `go test` but has no Output block, so it does
// not make a live API request during tests.
func ExampleClient_sessions() {
	client, err := devin.NewClient(os.Getenv("DEVIN_API_KEY"))
	if err != nil {
		return
	}
	ctx := context.Background()
	sessions := client.Organization(os.Getenv("DEVIN_ORG_ID")).Sessions
	created, _, err := sessions.Create(ctx, &devin.CreateSessionRequest{
		Prompt: "Summarize the open issues in this repository.",
	}, nil)
	if err != nil {
		var apiErr *devin.APIError
		if errors.As(err, &apiErr) {
			fmt.Println(apiErr.StatusCode, apiErr.RequestID)
		}
		return
	}
	_, _, err = sessions.Get(ctx, created.SessionID)
	if err != nil {
		return
	}
}

// sessionPages adapts the generated session list endpoint to the generic
// cursor iterator. Real callers can range over the returned sequence lazily.
func sessionPages(ctx context.Context, client *devin.Client, orgID string, first int) iter.Seq2[devin.SessionResponse, error] {
	return devin.Iterate(ctx, func(ctx context.Context, opts devin.ListOptions) (*devin.Page[devin.SessionResponse], error) {
		query := make(url.Values)
		if opts.First > 0 {
			query.Set("first", strconv.Itoa(opts.First))
		}
		if opts.After != "" {
			query.Set("after", opts.After)
		}
		result, _, err := client.Organization(orgID).Sessions.List(ctx, &devin.OrgSessionsListOptions{Extra: query})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, errors.New("devin: session list returned no response")
		}
		hasNextPage := result.HasNextPage != nil && *result.HasNextPage
		var endCursor *string
		if cursor, ok := result.EndCursor.Get(); ok {
			endCursor = &cursor
		}
		return &devin.Page[devin.SessionResponse]{
			Items:       result.Items,
			HasNextPage: hasNextPage,
			EndCursor:   endCursor,
		}, nil
	}, devin.ListOptions{First: first})
}

// This example is compiled by `go test` but has no Output block, so it does
// not perform a network request during tests.
func ExampleIterate_sessions() {
	client, err := devin.NewClient(os.Getenv("DEVIN_API_KEY"))
	if err != nil {
		return
	}
	for session, err := range sessionPages(context.Background(), client, os.Getenv("DEVIN_ORG_ID"), 100) {
		if err != nil {
			return
		}
		fmt.Println(session.SessionID)
	}
}
