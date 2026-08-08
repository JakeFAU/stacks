package drive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
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

func TestMeetingTimeFromTitleRequiresOneStrictLeadingISODate(t *testing.T) {
	want := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		title string
		want  *time.Time
	}{
		{name: "strict prefix", title: "[2026-07-20] Synthetic weekly meeting", want: &want},
		{name: "date elsewhere", title: "Synthetic weekly meeting 2026-07-20"},
		{name: "deadline-like title", title: "Synthetic deadline [2026-07-20]"},
		{name: "invalid calendar date", title: "[2026-02-30] Synthetic weekly meeting"},
		{name: "ambiguous prefixes", title: "[2026-07-20] [2026-07-21] Synthetic weekly meeting"},
		{name: "missing description", title: "[2026-07-20]"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := meetingTimeFromTitle(testCase.title)
			if testCase.want == nil {
				if got != nil {
					t.Fatalf("meetingTimeFromTitle() = %v, want unknown", got)
				}
				return
			}
			if got == nil || !got.Equal(*testCase.want) {
				t.Fatalf("meetingTimeFromTitle() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestListDerivesMeetingTimeFromStrictTitleMetadataNotModificationTime(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, `{
			"files":[{
				"id":"dated-document",
				"name":"[2026-07-20] Synthetic weekly meeting",
				"modifiedTime":"2026-07-22T14:30:00Z",
				"version":"42"
			},{
				"id":"undated-document",
				"name":"Synthetic weekly meeting 2026-07-21",
				"modifiedTime":"2026-07-23T14:30:00Z",
				"version":"43"
			}]
		}`), nil
	})}
	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))

	documents, err := client.List(context.Background(), "folder-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 {
		t.Fatalf("List() returned %d documents, want 2", len(documents))
	}
	wantMeetingTime := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	if documents[0].MeetingTime == nil || !documents[0].MeetingTime.Equal(wantMeetingTime) {
		t.Fatalf("dated MeetingTime = %v, want %v", documents[0].MeetingTime, wantMeetingTime)
	}
	if documents[0].MeetingTime.Equal(documents[0].ModifiedAt) {
		t.Fatal("meeting time must not be derived from Drive modification time")
	}
	if documents[1].MeetingTime != nil {
		t.Fatalf("undated MeetingTime = %v, want unknown for non-contract title", documents[1].MeetingTime)
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

func TestListRejectsRepeatedNextPageToken(t *testing.T) {
	const privatePageToken = "private-repeated-token"
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests > 2 {
			return nil, errors.New("pagination did not stop")
		}
		return jsonResponse(request, `{"nextPageToken":"`+privatePageToken+`"}`), nil
	})}

	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))
	_, err := client.List(context.Background(), "folder-1")
	if err == nil {
		t.Fatal("List() error = nil, want repeated page token rejection")
	}
	if err.Error() != "list direct Google Docs: invalid pagination response" {
		t.Fatalf("List() error = %q, want bounded pagination error", err)
	}
	if strings.Contains(err.Error(), privatePageToken) {
		t.Fatalf("List() error disclosed provider page token: %v", err)
	}
	if requests != 2 {
		t.Fatalf("List() made %d requests, want 2 before repeated token rejection", requests)
	}
}

func TestListRejectsNonconsecutiveNextPageTokenCycle(t *testing.T) {
	const (
		firstPageToken  = "synthetic-page-a"
		secondPageToken = "synthetic-page-b"
	)
	wantRequestTokens := []string{"", firstPageToken, secondPageToken}
	nextPageTokens := []string{firstPageToken, secondPageToken, firstPageToken}
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if requests >= len(wantRequestTokens) {
			return nil, errors.New("pagination did not stop")
		}
		if got := request.URL.Query().Get("pageToken"); got != wantRequestTokens[requests] {
			t.Errorf("request %d pageToken = %q, want %q", requests+1, got, wantRequestTokens[requests])
		}
		nextPageToken := nextPageTokens[requests]
		requests++
		return jsonResponse(request, `{"nextPageToken":"`+nextPageToken+`"}`), nil
	})}

	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))
	_, err := client.List(context.Background(), "folder-1")
	if err == nil {
		t.Fatal("List() error = nil, want nonconsecutive page token cycle rejection")
	}
	if err.Error() != "list direct Google Docs: invalid pagination response" {
		t.Fatalf("List() error = %q, want bounded pagination error", err)
	}
	if requests != len(wantRequestTokens) {
		t.Fatalf("List() made %d requests, want %d before cycle rejection", requests, len(wantRequestTokens))
	}
}

func TestListPrefersCallerCancellationToRepeatedNextPageToken(t *testing.T) {
	const repeatedPageToken = "synthetic-repeated-token"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 2 {
			cancel()
		}
		return jsonResponse(request, `{"nextPageToken":"`+repeatedPageToken+`"}`), nil
	})}

	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))
	_, err := client.List(ctx, "folder-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("List() did not preserve caller cancellation")
	}
	if requests != 2 {
		t.Fatalf("List() made %d requests, want 2 before cancellation", requests)
	}
}

func TestCheckCollectionRequestsOnlyFolderIdentityAndMIMEType(t *testing.T) {
	const folderID = "private-folder-id"
	var gotPath string
	var gotQuery url.Values
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotPath = request.URL.Path
		gotQuery = request.URL.Query()
		return jsonResponse(request, `{"id":"private-folder-id","mimeType":"application/vnd.google-apps.folder","name":"private folder title"}`), nil
	})}
	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))

	if err := client.CheckCollection(context.Background(), folderID); err != nil {
		t.Fatalf("CheckCollection() error = %v", err)
	}
	if gotPath != "/files/private-folder-id" {
		t.Errorf("Drive path = %q, want files.get path", gotPath)
	}
	if got := gotQuery.Get("fields"); got != "id,mimeType" {
		t.Errorf("fields = %q, want only folder identity and MIME type", got)
	}
	if strings.Contains(gotQuery.Get("fields"), "name") {
		t.Fatalf("fields = %q, must not request private folder title", gotQuery.Get("fields"))
	}
}

func TestCheckCollectionRejectsNonFolderWithoutDisclosingMetadata(t *testing.T) {
	const secretFolderID = "secret-not-a-folder-id"
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, `{"id":"`+secretFolderID+`","mimeType":"application/vnd.google-apps.document"}`), nil
	})}
	client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))

	err := client.CheckCollection(context.Background(), secretFolderID)
	if err == nil {
		t.Fatal("CheckCollection() error = nil, want non-folder error")
	}
	if err.Error() != "configured Google Drive collection is not a folder" {
		t.Fatalf("CheckCollection() error = %q, want bounded non-folder error", err)
	}
	if strings.Contains(err.Error(), secretFolderID) || strings.Contains(err.Error(), "application/vnd.google-apps.document") {
		t.Fatalf("CheckCollection() error disclosed provider metadata: %v", err)
	}
}

func TestCheckCollectionBoundsMissingAndDeniedErrors(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const (
				secretFolderID = "secret-folder-id"
				secretDetail   = "private provider detail"
			)
			httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return jsonStatusResponse(request, status, `{"error":{"code":`+strconv.Itoa(status)+`,"message":"`+secretDetail+`"}}`), nil
			})}
			client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))

			err := client.CheckCollection(context.Background(), secretFolderID)
			if err == nil {
				t.Fatal("CheckCollection() error = nil, want provider error")
			}
			want := "inspect Google Drive folder: Google API returned HTTP " + strconv.Itoa(status)
			if status == http.StatusForbidden {
				want = "inspect Google Drive folder: " + source.ErrAuthorization.Error()
			}
			if err.Error() != want {
				t.Fatalf("CheckCollection() error = %q, want %q", err, want)
			}
			if strings.Contains(err.Error(), secretFolderID) || strings.Contains(err.Error(), secretDetail) {
				t.Fatalf("CheckCollection() error disclosed private values: %v", err)
			}
		})
	}
}

func TestGoogleAPIErrorsPreserveBoundedAuthenticationAndAuthorizationTypes(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{status: http.StatusUnauthorized, want: source.ErrAuthentication},
		{status: http.StatusForbidden, want: source.ErrAuthorization},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			const privateDetail = "private provider authentication detail"
			httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return jsonStatusResponse(request, test.status, `{"error":{"code":`+strconv.Itoa(test.status)+`,"message":"`+privateDetail+`"}}`), nil
			})}
			client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))
			_, err := client.List(context.Background(), "private-folder")
			if !errors.Is(err, test.want) {
				t.Fatalf("List() error = %v, want typed auth failure", err)
			}
			if strings.Contains(err.Error(), privateDetail) || strings.Contains(err.Error(), "private-folder") {
				t.Fatalf("typed auth error leaked private provider text: %v", err)
			}
		})
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

func TestListRejectsMissingDocumentIDWithoutDisclosingMetadata(t *testing.T) {
	const privateTitle = "secret-provider-document-title"
	tests := []struct {
		name   string
		idJSON string
	}{
		{name: "omitted"},
		{name: "whitespace", idJSON: `"id":" \t",`},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return jsonResponse(request, `{"files":[{`+testCase.idJSON+`"name":"`+privateTitle+`"}]}`), nil
			})}
			client := newTestClient(t, httpClient, NewTabClassifier(nil, nil))

			_, err := client.List(context.Background(), "folder-1")
			if err == nil {
				t.Fatal("List() error = nil, want missing document ID rejection")
			}
			if err.Error() != "list direct Google Docs: invalid response" {
				t.Fatalf("List() error = %q, want bounded invalid-response error", err)
			}
			if strings.Contains(err.Error(), privateTitle) {
				t.Fatalf("List() error disclosed provider metadata: %v", err)
			}
		})
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
	if document.Revision != "revision-7" {
		t.Errorf("Get() revision = %q, want %q", document.Revision, "revision-7")
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
	return jsonStatusResponse(request, http.StatusOK, body)
}

func jsonStatusResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}
