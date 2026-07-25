package googledirectory

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"stacks/internal/entity"
)

const (
	testDirectoryReadMask = "metadata,names,emailAddresses"
	testDirectorySource   = "DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE"
	testDirectoryPageSize = "25"
)

func TestNewClientRejectsNonpositiveResultLimits(t *testing.T) {
	for _, maximumResults := range []int{0, -1} {
		client, err := NewClient(context.Background(), &http.Client{}, maximumResults)
		if err == nil || client != nil {
			t.Fatal("NewClient() accepted a nonpositive result limit")
		}
	}
}

func TestClientSearchSendsExactEmailPrefixRequest(t *testing.T) {
	query := entity.DirectoryQuery{Kind: entity.DirectoryQueryEmail, Email: "sample.person@example.test"}
	client := newTestClient(t, query.Email)

	result, err := client.Search(context.Background(), query)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != "" {
		t.Fatalf("Search() outcome = %q, want success", result.Outcome)
	}
	if len(result.Profiles) != 1 {
		t.Fatal("Search() did not convert the complete synthetic profile")
	}
	profile := result.Profiles[0]
	if profile.Provider != "google_people" || profile.SubjectID != "people/synthetic" || profile.Source != entity.DirectorySourceDomainProfile || profile.DisplayName != "Sample Person" {
		t.Fatal("Search() did not preserve the required directory profile fields")
	}
	if len(profile.Emails) != 1 || profile.Emails[0].Value != "sample.person@example.test" || !profile.Emails[0].Primary {
		t.Fatal("Search() did not normalize the required directory email")
	}
}

func TestClientSearchSendsExactNamePrefixRequest(t *testing.T) {
	query := entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample Person"}
	client := newTestClient(t, query.Name)

	result, err := client.Search(context.Background(), query)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != "" {
		t.Fatalf("Search() outcome = %q, want success", result.Outcome)
	}
}

func TestClientSearchFollowsPagesDeduplicatesAndOrdersResults(t *testing.T) {
	transport := &pageTransport{t: t, pages: map[string]string{
		"": `{
			"people": [
				{"resourceName":"people/2","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Sample Two","metadata":{"primary":true}}],"emailAddresses":[{"value":"zeta@example.test"},{"value":"alpha@example.test","metadata":{"primary":true}}]},
				{"resourceName":"people/1","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Sample One","metadata":{"primary":true}}],"emailAddresses":[{"value":"one@example.test","metadata":{"primary":true}}]}
			],
			"nextPageToken":"next"
		}`,
		"next": `{
			"people": [
				{"resourceName":"people/2","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Sample Two","metadata":{"primary":true}}],"emailAddresses":[{"value":"alpha@example.test","metadata":{"primary":true}},{"value":"zeta@example.test"}]},
				{"resourceName":"people/3","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Sample Three","metadata":{"primary":true}}],"emailAddresses":[{"value":"three@example.test","metadata":{"primary":true}}]}
			]
		}`,
	}}
	client := newPagedTestClient(t, transport, 4)

	result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != "" || len(result.Profiles) != 3 {
		t.Fatal("Search() did not return the bounded, deduplicated profiles")
	}
	if len(transport.tokens) != 2 || transport.tokens[0] != "" || transport.tokens[1] != "next" {
		t.Fatal("Search() did not follow exactly the permitted page token")
	}
	if result.Profiles[0].SubjectID != "people/1" || result.Profiles[1].SubjectID != "people/2" || result.Profiles[2].SubjectID != "people/3" {
		t.Fatal("Search() profile order depends on provider order")
	}
	emails := result.Profiles[1].Emails
	if len(emails) != 2 || emails[0].Value != "alpha@example.test" || !emails[0].Primary || emails[1].Value != "zeta@example.test" {
		t.Fatal("Search() did not deterministically deduplicate profile emails")
	}
}

func TestClientSearchReturnsLimitExceededBeforeNextPage(t *testing.T) {
	transport := &pageTransport{t: t, pages: map[string]string{
		"": `{
			"people": [
				{"resourceName":"people/1","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Sample One","metadata":{"primary":true}}],"emailAddresses":[{"value":"one@example.test","metadata":{"primary":true}}]},
				{"resourceName":"people/2","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Sample Two","metadata":{"primary":true}}],"emailAddresses":[{"value":"two@example.test","metadata":{"primary":true}}]}
			],
			"nextPageToken":"unbounded"
		}`,
	}}
	client := newPagedTestClient(t, transport, 2)

	result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != entity.DirectoryResultLimitExceeded || len(result.Profiles) != 0 {
		t.Fatal("Search() did not discard profiles when another page exceeds the result bound")
	}
	if len(transport.tokens) != 1 || transport.tokens[0] != "" {
		t.Fatal("Search() requested a page beyond the configured result bound")
	}
}

func TestClientSearchReturnsLimitExceededForOversizedPage(t *testing.T) {
	transport := &pageTransport{t: t, pages: map[string]string{
		"": `{
			"people": [
				{"resourceName":"people/1","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Sample One","metadata":{"primary":true}}],"emailAddresses":[{"value":"one@example.test","metadata":{"primary":true}}]},
				{"resourceName":"people/2","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Sample Two","metadata":{"primary":true}}],"emailAddresses":[{"value":"two@example.test","metadata":{"primary":true}}]}
			]
		}`,
	}}
	client := newPagedTestClient(t, transport, 1)

	result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != entity.DirectoryResultLimitExceeded || len(result.Profiles) != 0 {
		t.Fatal("Search() did not reject a page beyond the result bound")
	}
}

func TestClientSearchRejectsRepeatedNextPageToken(t *testing.T) {
	const maximumResults = 3
	requests := 0
	client, err := NewClient(context.Background(), &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests > maximumResults {
			return nil, errors.New("synthetic request cap")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"nextPageToken":"cycle"}`)),
			Request:    request,
		}, nil
	})}, maximumResults)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != entity.DirectoryInvalidResponse || len(result.Profiles) != 0 || requests != 2 {
		t.Fatal("Search() did not stop a repeated page token without authority")
	}
}

func TestClientSearchBoundsDistinctEmptyPagesByMaximumResults(t *testing.T) {
	const maximumResults = 3
	nextPageTokens := map[string]string{"": "first", "first": "second", "second": "third"}
	requests := 0
	client, err := NewClient(context.Background(), &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests > maximumResults {
			return nil, errors.New("synthetic request cap")
		}
		nextPageToken := nextPageTokens[request.URL.Query().Get("pageToken")]
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"nextPageToken":"` + nextPageToken + `"}`)),
			Request:    request,
		}, nil
	})}, maximumResults)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != entity.DirectoryResultLimitExceeded || len(result.Profiles) != 0 || requests != maximumResults {
		t.Fatal("Search() did not bound distinct empty pages without authority")
	}
}

func TestClientSearchClassifiesGoogleAPIErrors(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		statusCode int
		want       entity.DirectoryOutcome
		retryAfter time.Duration
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, want: entity.DirectoryUnauthorized},
		{name: "forbidden", statusCode: http.StatusForbidden, want: entity.DirectoryForbidden},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, want: entity.DirectoryRateLimited, retryAfter: 7 * time.Second},
		{name: "internal", statusCode: http.StatusInternalServerError, want: entity.DirectoryUnavailable},
		{name: "bad gateway", statusCode: http.StatusBadGateway, want: entity.DirectoryUnavailable},
		{name: "service unavailable", statusCode: http.StatusServiceUnavailable, want: entity.DirectoryUnavailable},
		{name: "gateway timeout", statusCode: http.StatusGatewayTimeout, want: entity.DirectoryUnavailable},
		{name: "invalid request", statusCode: http.StatusBadRequest, want: entity.DirectoryInvalidResponse},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			header := make(http.Header)
			if testCase.retryAfter > 0 {
				header.Set("Retry-After", "7")
			}
			client, err := NewClient(context.Background(), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, &googleapi.Error{Code: testCase.statusCode, Header: header}
			})}, 1)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample"})
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if result.Outcome != testCase.want || result.RetryAfter != testCase.retryAfter || len(result.Profiles) != 0 {
				t.Fatal("Search() did not return the required bounded provider outcome")
			}
		})
	}
}

func TestClientSearchReturnsCanonicalContextErrors(t *testing.T) {
	client, err := NewClient(context.Background(), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("Search() issued a request after context cancellation")
		return nil, nil
	})}, 1)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Search(canceled, entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample"}); !errors.Is(err, context.Canceled) {
		t.Fatal("Search() did not return context.Canceled")
	}

	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := client.Search(expired, entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Search() did not return context.DeadlineExceeded")
	}
}

func TestClientSearchRedactsTransportFailure(t *testing.T) {
	client, err := NewClient(context.Background(), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("synthetic provider transport detail")
	})}, 1)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Sample"})
	if err != nil || result.Outcome != entity.DirectoryUnavailable || len(result.Profiles) != 0 {
		t.Fatal("Search() did not redact an unavailable transport failure")
	}
}

func TestClientSearchFiltersEmailPrefixResultsExactly(t *testing.T) {
	transport := &pageTransport{t: t, pages: map[string]string{
		"": `{
			"people": [
				{"resourceName":"people/prefix","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Prefix Result","metadata":{"primary":true}}],"emailAddresses":[{"value":"exactly@example.test","metadata":{"primary":true}}]},
				{"resourceName":"people/exact","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Exact Result","metadata":{"primary":true}}],"emailAddresses":[{"value":"exact@example.test","metadata":{"primary":true}}]}
			]
		}`,
	}}
	client := newPagedTestClient(t, transport, 2)

	result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryEmail, Email: "Exact@Example.test"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != "" || len(result.Profiles) != 1 || result.Profiles[0].SubjectID != "people/exact" {
		t.Fatal("Search() did not exclude an email prefix match")
	}
}

func TestClientSearchReturnsNoMatchForEmailPrefixWithoutExactResult(t *testing.T) {
	transport := &pageTransport{t: t, pages: map[string]string{
		"": `{
			"people": [
				{"resourceName":"people/prefix","metadata":{"sources":[{"type":"DOMAIN_PROFILE"}]},"names":[{"displayName":"Prefix Result","metadata":{"primary":true}}],"emailAddresses":[{"value":"exactly@example.test","metadata":{"primary":true}}]}
			]
		}`,
	}}
	client := newPagedTestClient(t, transport, 1)

	result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryEmail, Email: "exact@example.test"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != entity.DirectoryNoMatch || len(result.Profiles) != 0 {
		t.Fatal("Search() treated an email prefix result as an exact match")
	}
}

func TestClientSearchRejectsPartialProviderRecord(t *testing.T) {
	transport := &pageTransport{t: t, pages: map[string]string{
		"": `{
			"people": [{
				"resourceName":"people/partial",
				"metadata":{"sources":[null,{"type":"DOMAIN_PROFILE"}]},
				"names":[{"displayName":"Partial Result","metadata":{"primary":true}}],
				"emailAddresses":[{"value":"partial@example.test","metadata":{"primary":true}}]
			}]
		}`,
	}}
	client := newPagedTestClient(t, transport, 1)

	result, err := client.Search(context.Background(), entity.DirectoryQuery{Kind: entity.DirectoryQueryName, Name: "Partial"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Outcome != entity.DirectoryInvalidResponse || len(result.Profiles) != 0 {
		t.Fatal("Search() allowed a partial provider record to earn authority")
	}
}

func newTestClient(t *testing.T, wantQuery string) *Client {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Errorf("request method = %q, want GET", request.Method)
		}
		if request.URL.Path != "/v1/people:searchDirectoryPeople" {
			t.Errorf("request path = %q, want directory search path", request.URL.Path)
		}
		values := request.URL.Query()
		if values.Get("readMask") != testDirectoryReadMask {
			t.Errorf("request readMask = %q, want required mask", values.Get("readMask"))
		}
		if values.Get("sources") != testDirectorySource {
			t.Errorf("request sources = %q, want domain profile source", values.Get("sources"))
		}
		if values.Get("pageSize") != testDirectoryPageSize {
			t.Errorf("request pageSize = %q, want default page size", values.Get("pageSize"))
		}
		if values.Get("query") != wantQuery {
			t.Errorf("request query did not preserve supplied prefix")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"people": [{
					"resourceName": "people/synthetic",
					"metadata": {"sources": [{"type": "DOMAIN_PROFILE"}]},
					"names": [{"displayName": "Sample Person", "metadata": {"primary": true}}],
					"emailAddresses": [{"value": "sample.person@example.test", "metadata": {"primary": true}}]
				}]
			}`)),
			Request: request,
		}, nil
	})}
	client, err := NewClient(context.Background(), httpClient, 25)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type pageTransport struct {
	t      *testing.T
	pages  map[string]string
	tokens []string
}

func (transport *pageTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.tokens = append(transport.tokens, request.URL.Query().Get("pageToken"))
	body, found := transport.pages[transport.tokens[len(transport.tokens)-1]]
	if !found {
		transport.t.Error("directory search requested an unconfigured page")
		body = `{}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func newPagedTestClient(t *testing.T, transport *pageTransport, maximumResults int) *Client {
	t.Helper()
	client, err := NewClient(context.Background(), &http.Client{Transport: transport}, maximumResults)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
