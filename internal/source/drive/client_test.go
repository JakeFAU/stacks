package drive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/docs/v1"
	googledrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"stacks/internal/source"
)

func TestListQueriesDirectGoogleDocsInExactFolder(t *testing.T) {
	const folderID = "folder 'synthetic'"
	var gotQuery url.Values
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotQuery = request.URL.Query()
		return jsonResponse(request, `{
			"files":[{
				"id":"document-1",
				"name":"Synthetic weekly meeting",
				"modifiedTime":"2026-07-20T14:30:00Z",
				"version":"42",
				"webViewLink":"https://docs.google.test/document/d/document-1/edit"
			}]
		}`), nil
	})}

	client := newTestClient(t, httpClient, NewTabClassifier([]string{"Transcript"}, nil))
	documents, err := client.List(context.Background(), folderID)
	if err != nil {
		t.Fatal(err)
	}

	wantQuery := "'folder \\'synthetic\\'' in parents and trashed = false and mimeType = 'application/vnd.google-apps.document'"
	if got := gotQuery.Get("q"); got != wantQuery {
		t.Errorf("Drive query = %q, want %q", got, wantQuery)
	}
	if fields := gotQuery.Get("fields"); fields != "nextPageToken,files(id,name,modifiedTime,version,webViewLink)" {
		t.Errorf("Drive fields = %q, want minimal source fields", fields)
	}

	wantModified := time.Date(2026, time.July, 20, 14, 30, 0, 0, time.UTC)
	want := []source.Document{{
		Provider:   "drive",
		ID:         "document-1",
		Title:      "Synthetic weekly meeting",
		Locator:    "https://docs.google.test/document/d/document-1/edit",
		Version:    "42",
		ModifiedAt: wantModified,
	}}
	if !reflect.DeepEqual(documents, want) {
		t.Errorf("List() = %#v, want %#v", documents, want)
	}
}

func TestListFollowsDrivePagination(t *testing.T) {
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return jsonResponse(request, `{"nextPageToken":"page-2","files":[{"id":"document-1"}]}`), nil
		}
		if got := request.URL.Query().Get("pageToken"); got != "page-2" {
			t.Errorf("pageToken = %q, want page-2", got)
		}
		return jsonResponse(request, `{"files":[{"id":"document-2"}]}`), nil
	})}

	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))
	documents, err := client.List(context.Background(), "folder-1")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(documents) != 2 {
		t.Fatalf("List() made %d requests and returned %d documents, want 2 and 2", requests, len(documents))
	}
}

func TestGetRepresentativeLimitsDirectGoogleDocLookupToOnePageAndOneID(t *testing.T) {
	const folderID = "folder 'synthetic'"
	requests := 0
	var gotQuery url.Values
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		gotQuery = request.URL.Query()
		return jsonResponse(request, `{"nextPageToken":"must-not-be-followed","files":[{"id":"document-1"}]}`), nil
	})}
	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))

	document, found, err := client.GetRepresentative(context.Background(), folderID)
	if err != nil {
		t.Fatalf("GetRepresentative() error = %v", err)
	}
	if !found || document.ID != "document-1" {
		t.Fatalf("GetRepresentative() = (%#v, %t), want document-1, true", document, found)
	}
	if requests != 1 {
		t.Fatalf("GetRepresentative() requests = %d, want 1", requests)
	}
	wantQuery := "'folder \\'synthetic\\'' in parents and trashed = false and mimeType = 'application/vnd.google-apps.document'"
	if got := gotQuery.Get("q"); got != wantQuery {
		t.Errorf("Drive query = %q, want %q", got, wantQuery)
	}
	if got := gotQuery.Get("pageSize"); got != "1" {
		t.Errorf("pageSize = %q, want 1", got)
	}
	if got := gotQuery.Get("fields"); got != "files(id)" {
		t.Errorf("fields = %q, want minimal representative fields", got)
	}
}

func TestListRedactsTransportErrors(t *testing.T) {
	const secret = "secret-transport-detail"
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(secret)
	})}
	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))

	_, err := client.List(context.Background(), "folder-1")
	if err == nil {
		t.Fatal("List() error = nil, want transport error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("List() error disclosed transport detail: %v", err)
	}
}

func TestListRedactsInvalidModifiedTime(t *testing.T) {
	const (
		secretDocumentID = "secret-provider-document-id"
		secretModifiedAt = "secret-invalid-modified-time"
	)
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, `{"files":[{"id":"`+secretDocumentID+`","modifiedTime":"`+secretModifiedAt+`"}]}`), nil
	})}
	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))

	_, err := client.List(context.Background(), "folder-1")
	if err == nil {
		t.Fatal("List() error = nil, want invalid modified time error")
	}
	if strings.Contains(err.Error(), secretDocumentID) || strings.Contains(err.Error(), secretModifiedAt) {
		t.Fatalf("List() error disclosed provider-controlled values: %v", err)
	}
}

func TestGetRedactsDocumentIDFromFetchErrors(t *testing.T) {
	const (
		secretDocumentID = "secret-fetch-document-id"
		secretDetail     = "secret-provider-error-detail"
	)
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(secretDetail)
	})}
	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))

	_, err := client.Get(context.Background(), secretDocumentID)
	if err == nil {
		t.Fatal("Get() error = nil, want fetch error")
	}
	if strings.Contains(err.Error(), secretDocumentID) || strings.Contains(err.Error(), secretDetail) {
		t.Fatalf("Get() error disclosed provider-controlled values: %v", err)
	}
}

func TestGetRedactsDocumentIDFromTabConversionErrors(t *testing.T) {
	const secretDocumentID = "secret-conversion-document-id"
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, `{"documentId":"`+secretDocumentID+`","tabs":[]}`), nil
	})}
	client := newTestClient(t, httpClient, NewTabClassifier([]string{"Transcript"}, nil))

	_, err := client.Get(context.Background(), secretDocumentID)
	if err == nil {
		t.Fatal("Get() error = nil, want tab conversion error")
	}
	if strings.Contains(err.Error(), secretDocumentID) {
		t.Fatalf("Get() error disclosed provider document ID: %v", err)
	}
}

func TestGetRequestsAllTabsAndConvertsDocument(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotPath = request.URL.Path
		gotQuery = request.URL.Query()
		return jsonResponse(request, `{
			"documentId":"document-1",
			"title":"Synthetic weekly meeting",
			"revisionId":"revision-7",
			"tabs":[{
				"tabProperties":{"tabId":"notes","title":"Meeting notes"},
				"documentTab":{"body":{"content":[{"paragraph":{"elements":[{"textRun":{"content":"summary"}}]}}]}}
			},{
				"tabProperties":{"tabId":"transcript","title":"Transcript"},
				"documentTab":{"body":{"content":[{"paragraph":{"elements":[{"textRun":{"content":"Alex: synthetic words"}}]}}]}}
			}]
		}`), nil
	})}

	client := newTestClient(t, httpClient, NewTabClassifier([]string{"Transcript"}, []string{"Meeting notes"}))
	document, err := client.Get(context.Background(), "document-1")
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/v1/documents/document-1" {
		t.Errorf("Docs path = %q, want %q", gotPath, "/v1/documents/document-1")
	}
	if got := gotQuery.Get("includeTabsContent"); got != "true" {
		t.Errorf("includeTabsContent = %q, want true", got)
	}
	if document.Provider != "drive" || document.ID != "document-1" || document.Title != "Synthetic weekly meeting" {
		t.Errorf("Get() identity = %#v, want converted Drive document", document)
	}
	if document.Version != "revision-7" {
		t.Errorf("Get() version = %q, want %q", document.Version, "revision-7")
	}
	if document.Locator != "https://docs.google.com/document/d/document-1/edit" {
		t.Errorf("Get() locator = %q, want tab-capable Docs locator", document.Locator)
	}
	if len(document.Tabs) != 2 || document.Tabs[1].Role != source.TabRoleTranscript {
		t.Errorf("Get() tabs = %#v, want separate notes and transcript tabs", document.Tabs)
	}
}

func newTestClient(t *testing.T, httpClient *http.Client, classifier TabClassifier) *Client {
	t.Helper()
	ctx := context.Background()
	driveService, err := googledrive.NewService(ctx,
		option.WithHTTPClient(httpClient),
		option.WithEndpoint("https://google.test/"),
	)
	if err != nil {
		t.Fatalf("create Drive service: %v", err)
	}
	docsService, err := docs.NewService(ctx,
		option.WithHTTPClient(httpClient),
		option.WithEndpoint("https://google.test/"),
	)
	if err != nil {
		t.Fatalf("create Docs service: %v", err)
	}
	return newClient(driveService, docsService, classifier)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func jsonResponse(request *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}
