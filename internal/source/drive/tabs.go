// Package drive translates Google Docs tab trees into source-neutral tabs.
package drive

import (
	"fmt"
	"strings"

	"google.golang.org/api/docs/v1"

	"stacks/internal/source"
)

// TabClassifier deterministically classifies tabs using explicitly configured
// user-visible titles.
type TabClassifier struct {
	transcriptTitles map[string]struct{}
	notesTitles      map[string]struct{}
}

// NewTabClassifier constructs a title-based tab classifier. Configuration
// validation owns detecting overlap between transcript and notes title sets.
func NewTabClassifier(transcriptTitles, notesTitles []string) TabClassifier {
	return TabClassifier{
		transcriptTitles: normalizedTitleSet(transcriptTitles),
		notesTitles:      normalizedTitleSet(notesTitles),
	}
}

// Classify returns the configured role for title, or Other when the title is
// not configured. Transcript wins only for invalid overlapping configuration;
// command configuration rejects that condition before a source is constructed.
func (classifier TabClassifier) Classify(title string) source.TabRole {
	normalized := normalizeTitle(title)
	if _, exists := classifier.transcriptTitles[normalized]; exists {
		return source.TabRoleTranscript
	}
	if _, exists := classifier.notesTitles[normalized]; exists {
		return source.TabRoleGeminiNotes
	}
	return source.TabRoleOther
}

// FlattenTabs recursively traverses Google Docs tabs in their returned UI
// order, extracts local text, and requires exactly one configured transcript.
func FlattenTabs(roots []*docs.Tab, classifier TabClassifier) ([]source.Tab, error) {
	tabs := make([]source.Tab, 0, len(roots))
	for _, root := range roots {
		if err := flattenTab(root, "", nil, classifier, &tabs); err != nil {
			return nil, err
		}
	}

	transcriptCount := 0
	for _, tab := range tabs {
		if tab.Role == source.TabRoleTranscript {
			transcriptCount++
		}
	}
	if transcriptCount != 1 {
		return nil, fmt.Errorf("document must contain exactly one transcript tab, found %d", transcriptCount)
	}
	return tabs, nil
}

func flattenTab(
	tab *docs.Tab,
	parentID string,
	parentPath []string,
	classifier TabClassifier,
	flattened *[]source.Tab,
) error {
	if tab == nil {
		return fmt.Errorf("document tab is required")
	}
	if tab.TabProperties == nil {
		return fmt.Errorf("document tab properties are required")
	}

	id := strings.TrimSpace(tab.TabProperties.TabId)
	if id == "" {
		return fmt.Errorf("document tab ID is required")
	}
	title := strings.TrimSpace(tab.TabProperties.Title)
	if title == "" {
		return fmt.Errorf("document tab title is required")
	}

	path := append(append([]string(nil), parentPath...), title)
	*flattened = append(*flattened, source.Tab{
		ID:       id,
		Title:    title,
		ParentID: parentID,
		Path:     path,
		Order:    len(*flattened),
		Role:     classifier.Classify(title),
		Text:     extractTabText(tab.DocumentTab),
	})

	for _, child := range tab.ChildTabs {
		if err := flattenTab(child, id, path, classifier, flattened); err != nil {
			return err
		}
	}
	return nil
}

func extractTabText(tab *docs.DocumentTab) string {
	if tab == nil || tab.Body == nil {
		return ""
	}
	return extractStructuralText(tab.Body.Content)
}

func extractStructuralText(elements []*docs.StructuralElement) string {
	var text strings.Builder
	for _, element := range elements {
		if element == nil {
			continue
		}
		if element.Paragraph != nil {
			for _, paragraphElement := range element.Paragraph.Elements {
				if paragraphElement != nil && paragraphElement.TextRun != nil {
					text.WriteString(paragraphElement.TextRun.Content)
				}
			}
		}
		if element.Table != nil {
			for _, row := range element.Table.TableRows {
				if row == nil {
					continue
				}
				for _, cell := range row.TableCells {
					if cell != nil {
						text.WriteString(extractStructuralText(cell.Content))
					}
				}
			}
		}
		if element.TableOfContents != nil {
			text.WriteString(extractStructuralText(element.TableOfContents.Content))
		}
	}
	return text.String()
}

func normalizedTitleSet(titles []string) map[string]struct{} {
	set := make(map[string]struct{}, len(titles))
	for _, title := range titles {
		if normalized := normalizeTitle(title); normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	return set
}

func normalizeTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}
