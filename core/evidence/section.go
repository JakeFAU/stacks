package evidence

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// SectionInput contains provider-neutral structured content from one document.
type SectionInput struct {
	ID, Title, ParentID string
	Path                []string
	Order               int
	Role                string
	Text                string
}

// Section is immutable ordered source content within a document version.
type Section struct {
	id, title, parentID string
	path                []string
	order               int
	role, text          string
}

// NewSection validates and constructs immutable document content.
func NewSection(input SectionInput) (Section, error) {
	section := Section{id: strings.TrimSpace(input.ID), title: strings.TrimSpace(input.Title), parentID: strings.TrimSpace(input.ParentID), order: input.Order, role: strings.TrimSpace(input.Role), text: input.Text}
	if section.id == "" {
		return Section{}, fmt.Errorf("ID is required")
	}
	if section.title == "" {
		return Section{}, fmt.Errorf("title is required")
	}
	if section.role == "" {
		return Section{}, fmt.Errorf("role is required")
	}
	if section.order < 0 {
		return Section{}, fmt.Errorf("order must not be negative")
	}
	if !utf8.ValidString(section.text) {
		return Section{}, fmt.Errorf("text must be valid UTF-8")
	}
	section.path = make([]string, len(input.Path))
	for index, pathTitle := range input.Path {
		section.path[index] = strings.TrimSpace(pathTitle)
		if section.path[index] == "" {
			return Section{}, fmt.Errorf("path title is required")
		}
	}
	return section, nil
}

func (section Section) ID() string       { return section.id }
func (section Section) Title() string    { return section.title }
func (section Section) ParentID() string { return section.parentID }
func (section Section) Path() []string   { return append([]string(nil), section.path...) }
func (section Section) Order() int       { return section.order }
func (section Section) Role() string     { return section.role }
func (section Section) Text() string     { return section.text }
