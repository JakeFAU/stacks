package drive

import (
	"reflect"
	"strings"
	"testing"

	"google.golang.org/api/docs/v1"

	"stacks/internal/source"
)

func TestFlattenTabsFindsTranscriptAfterNotes(t *testing.T) {
	roots := []*docs.Tab{
		tab("t.notes", "Meeting notes", "summary"),
		tab("t.transcript", "Transcript", "Alex: synthetic words"),
	}

	got, err := FlattenTabs(roots, NewTabClassifier([]string{"transcript"}, []string{"meeting notes"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("FlattenTabs() returned %d tabs, want 2", len(got))
	}
	if got[1].Role != source.TabRoleTranscript {
		t.Fatalf("transcript role = %v, want %v", got[1].Role, source.TabRoleTranscript)
	}
	if got[0].Role != source.TabRoleGeminiNotes {
		t.Fatalf("notes role = %v, want %v", got[0].Role, source.TabRoleGeminiNotes)
	}
}

func TestFlattenTabsFindsNestedTranscriptInUIOrder(t *testing.T) {
	nestedTranscript := tab("t.transcript", "Transcript", "Alex: synthetic words")
	roots := []*docs.Tab{
		withChildren(tab("t.notes", "Meeting notes", "summary"), nestedTranscript),
		tab("t.follow-up", "Follow-up", "next steps"),
	}

	got, err := FlattenTabs(roots, NewTabClassifier([]string{"transcript"}, []string{"meeting notes"}))
	if err != nil {
		t.Fatal(err)
	}

	if got[1].ID != "t.transcript" {
		t.Fatalf("nested tab ID = %q, want %q", got[1].ID, "t.transcript")
	}
	if got[1].ParentID != "t.notes" {
		t.Errorf("nested tab parent = %q, want %q", got[1].ParentID, "t.notes")
	}
	if got[1].Order != 1 {
		t.Errorf("nested tab order = %d, want 1", got[1].Order)
	}
	if !reflect.DeepEqual(got[1].Path, []string{"Meeting notes", "Transcript"}) {
		t.Errorf("nested tab path = %#v, want %#v", got[1].Path, []string{"Meeting notes", "Transcript"})
	}
}

func TestFlattenTabsRejectsAmbiguousTranscriptTitles(t *testing.T) {
	roots := []*docs.Tab{
		tab("t.one", "Transcript", "first"),
		tab("t.two", "Transcript", "second"),
	}

	_, err := FlattenTabs(roots, NewTabClassifier([]string{"transcript"}, nil))
	if err == nil || !strings.Contains(err.Error(), "exactly one transcript") {
		t.Fatalf("FlattenTabs() error = %v, want ambiguous transcript error", err)
	}
}

func TestFlattenTabsRejectsMissingTranscript(t *testing.T) {
	roots := []*docs.Tab{tab("t.notes", "Meeting notes", "summary")}

	_, err := FlattenTabs(roots, NewTabClassifier([]string{"transcript"}, []string{"meeting notes"}))
	if err == nil || !strings.Contains(err.Error(), "exactly one transcript") {
		t.Fatalf("FlattenTabs() error = %v, want missing transcript error", err)
	}
}

func TestFlattenTabsExtractsParagraphTableAndTableOfContentsText(t *testing.T) {
	root := tabWithContent("t.transcript", "Transcript", []*docs.StructuralElement{
		paragraph("opening\n"),
		{
			Table: &docs.Table{TableRows: []*docs.TableRow{{
				TableCells: []*docs.TableCell{{
					Content: []*docs.StructuralElement{paragraph("table cell\n")},
				}},
			}}},
		},
		{TableOfContents: &docs.TableOfContents{Content: []*docs.StructuralElement{paragraph("outline\n")}}},
	})

	got, err := FlattenTabs([]*docs.Tab{root}, NewTabClassifier([]string{"transcript"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Text != "opening\ntable cell\noutline\n" {
		t.Errorf("tab text = %q, want ordered paragraph, table, and TOC text", got[0].Text)
	}
}

func tab(id, title, text string) *docs.Tab {
	return tabWithContent(id, title, []*docs.StructuralElement{paragraph(text)})
}

func tabWithContent(id, title string, content []*docs.StructuralElement) *docs.Tab {
	return &docs.Tab{
		TabProperties: &docs.TabProperties{TabId: id, Title: title},
		DocumentTab:   &docs.DocumentTab{Body: &docs.Body{Content: content}},
	}
}

func withChildren(tab *docs.Tab, children ...*docs.Tab) *docs.Tab {
	tab.ChildTabs = children
	return tab
}

func paragraph(text string) *docs.StructuralElement {
	return &docs.StructuralElement{Paragraph: &docs.Paragraph{
		Elements: []*docs.ParagraphElement{{TextRun: &docs.TextRun{Content: text}}},
	}}
}
