package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"

	"stacks/internal/query"
)

func renderQueryText(result query.Result) ([]byte, error) {
	if err := query.ValidateResult(result); err != nil {
		return nil, fmt.Errorf("render query text: invalid result: %w", err)
	}
	trend, ok := result.Payload.Trend()
	if !ok || result.Intent != temporal.IntentTrendComparison {
		return nil, fmt.Errorf("render query text: trend result is required")
	}

	var rendered strings.Builder
	fmt.Fprintf(&rendered, "intent: %s\n", result.Intent)
	fmt.Fprintf(&rendered, "entities: %s\n", textList(entityIDStrings(result)))
	fmt.Fprintf(&rendered, "entity match: %s\n", result.EntityMatch)
	fmt.Fprintf(&rendered, "predicates: %s\n", textList(predicateStrings(result)))
	if err := renderTextSelection(&rendered, trend.Before.Selection); err != nil {
		return nil, err
	}
	if err := renderTextSelection(&rendered, trend.After.Selection); err != nil {
		return nil, err
	}
	if err := renderTextKnowledge(&rendered, result.KnowledgeScope); err != nil {
		return nil, err
	}
	fmt.Fprintf(&rendered, "limit: %d\n", result.Limit)

	if err := renderTextFacts(&rendered, "before facts", trend.Before.Facts, "  "); err != nil {
		return nil, err
	}
	if err := renderTextUnresolved(&rendered, "before unresolved", trend.Before.Unresolved, "  "); err != nil {
		return nil, err
	}
	if err := renderTextFacts(&rendered, "after facts", trend.After.Facts, "  "); err != nil {
		return nil, err
	}
	if err := renderTextUnresolved(&rendered, "after unresolved", trend.After.Unresolved, "  "); err != nil {
		return nil, err
	}
	if err := renderTextChanges(&rendered, trend.Changes); err != nil {
		return nil, err
	}
	if err := renderTextStateKeys(&rendered, trend.UnresolvedKeys); err != nil {
		return nil, err
	}
	renderTextGaps(&rendered, result.Gaps)
	return []byte(rendered.String()), nil
}

func renderTextSelection(output *strings.Builder, value temporal.TemporalSelection) error {
	switch value.Kind() {
	case temporal.SelectionPoint:
		at, ok := value.Point()
		if !ok {
			return fmt.Errorf("render query text: point selection is invalid")
		}
		fmt.Fprintf(output, "%s point: %s\n", value.Label(), queryTime(at))
	case temporal.SelectionWindow:
		start, end, ok := value.Window()
		if !ok {
			return fmt.Errorf("render query text: window selection is invalid")
		}
		fmt.Fprintf(output, "%s window: [%s, %s)\n", value.Label(), queryTime(start), queryTime(end))
	default:
		return fmt.Errorf("render query text: selection kind is invalid")
	}
	return nil
}

func renderTextKnowledge(output *strings.Builder, value temporal.KnowledgeScope) error {
	switch value.Kind() {
	case temporal.KnowledgeCurrent:
		output.WriteString("knowledge scope: current\n")
	case temporal.KnowledgeAsOf:
		at, ok := value.AsOf()
		if !ok {
			return fmt.Errorf("render query text: knowledge scope is invalid")
		}
		fmt.Fprintf(output, "knowledge scope: as-of %s\n", queryTime(at))
	default:
		return fmt.Errorf("render query text: knowledge scope kind is invalid")
	}
	return nil
}

func renderTextFacts(output *strings.Builder, heading string, values []query.Fact, indent string) error {
	fmt.Fprintf(output, "%s:\n", heading)
	if len(values) == 0 {
		fmt.Fprintf(output, "%s(none)\n", indent)
		return nil
	}
	for _, value := range values {
		if err := renderTextFact(output, value, indent); err != nil {
			return err
		}
	}
	return nil
}

func renderTextFact(output *strings.Builder, value query.Fact, indent string) error {
	key, err := textStateKey(value.Key)
	if err != nil {
		return err
	}
	factValue, err := textTerm(value.Value)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "%s- key: %s value=%s\n", indent, key, factValue)
	fmt.Fprintf(output, "%s  contributions:\n", indent)
	for _, contribution := range value.Contributions {
		validTime, err := textExtent(contribution.ValidTime)
		if err != nil {
			return err
		}
		fmt.Fprintf(
			output,
			"%s    - observation_id=%s status=%s valid_time=%s recorded_at=%s derivation_method=%s derivation_version=%s",
			indent,
			contribution.ObservationID,
			contribution.Status,
			validTime,
			queryTime(contribution.RecordedAt),
			contribution.Derivation.Method,
			contribution.Derivation.Version,
		)
		if contribution.Derivation.RunID != "" {
			fmt.Fprintf(output, " run_id=%s", contribution.Derivation.RunID)
		}
		if contribution.Derivation.Model != "" {
			fmt.Fprintf(output, " model=%s prompt_version=%s", contribution.Derivation.Model, contribution.Derivation.PromptVersion)
		}
		if contribution.SubjectGroundingMentionID != "" {
			fmt.Fprintf(output, " subject_grounding_mention_id=%s", contribution.SubjectGroundingMentionID)
		}
		if contribution.ObjectGroundingMentionID != "" {
			fmt.Fprintf(output, " object_grounding_mention_id=%s", contribution.ObjectGroundingMentionID)
		}
		output.WriteByte('\n')
	}
	renderTextCitations(output, "supporting citations", value.SupportingCitations, indent+"  ")
	renderTextCitations(output, "contradicting citations", value.ContradictingCitations, indent+"  ")
	return nil
}

func renderTextCitations(output *strings.Builder, heading string, values []query.Citation, indent string) {
	fmt.Fprintf(output, "%s%s:\n", indent, heading)
	if len(values) == 0 {
		fmt.Fprintf(output, "%s  (none)\n", indent)
		return
	}
	for _, citation := range values {
		fmt.Fprintf(
			output,
			"%s  - evidence_id=%s role=%s source_document_id=%s document_version_id=%s section_id=%s section_title=%q section_path=%q section_order=%d section_role=%s offsets=%d:%d",
			indent,
			citation.EvidenceID,
			citation.Role,
			citation.SourceDocumentID,
			citation.DocumentVersionID,
			citation.SectionID,
			citation.SectionTitle,
			citation.SectionPath,
			citation.SectionOrder,
			citation.SectionRole,
			citation.StartOffset,
			citation.EndOffset,
		)
		if citation.Locator != "" {
			fmt.Fprintf(output, " locator=%q", citation.Locator)
		}
		if citation.Text != "" {
			fmt.Fprintf(output, " text=%q", citation.Text)
		}
		output.WriteByte('\n')
	}
}

func renderTextUnresolved(
	output *strings.Builder,
	heading string,
	values []query.UnresolvedItem,
	indent string,
) error {
	fmt.Fprintf(output, "%s:\n", heading)
	if len(values) == 0 {
		fmt.Fprintf(output, "%s(none)\n", indent)
		return nil
	}
	for _, value := range values {
		key, err := textStateKey(value.Key)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "%s- key: %s reason=%s\n", indent, key, value.Reason)
		fmt.Fprintf(output, "%s  candidates:\n", indent)
		for _, candidate := range value.Candidates {
			if err := renderTextFact(output, candidate, indent+"    "); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderTextChanges(output *strings.Builder, values []query.Change) error {
	output.WriteString("changes:\n")
	if len(values) == 0 {
		output.WriteString("  (none)\n")
		return nil
	}
	for _, value := range values {
		key, err := textStateKey(value.Key)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "  - kind=%s key: %s\n", value.Kind, key)
		if value.Before != nil {
			output.WriteString("    before:\n")
			if err := renderTextFact(output, *value.Before, "      "); err != nil {
				return err
			}
		}
		if value.After != nil {
			output.WriteString("    after:\n")
			if err := renderTextFact(output, *value.After, "      "); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderTextStateKeys(output *strings.Builder, values []temporal.StateKey) error {
	output.WriteString("unresolved keys:\n")
	if len(values) == 0 {
		output.WriteString("  (none)\n")
		return nil
	}
	for _, value := range values {
		key, err := textStateKey(value)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "  - %s\n", key)
	}
	return nil
}

func renderTextGaps(output *strings.Builder, values []query.Gap) {
	output.WriteString("gaps:\n")
	if len(values) == 0 {
		output.WriteString("  (none)\n")
		return
	}
	for _, value := range values {
		fmt.Fprintf(output, "  - kind=%s", value.Kind)
		if value.EntityID != "" {
			fmt.Fprintf(output, " entity_id=%s", value.EntityID)
		}
		if value.Predicate != "" {
			fmt.Fprintf(output, " predicate=%s", value.Predicate)
		}
		if value.SelectionLabel != "" {
			fmt.Fprintf(output, " selection_label=%s", value.SelectionLabel)
		}
		output.WriteByte('\n')
	}
}

func textStateKey(value temporal.StateKey) (string, error) {
	subject, err := textTerm(value.Subject)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("subject=%s predicate=%s", subject, value.Predicate), nil
}

func textTerm(value observation.Term) (string, error) {
	switch value.Kind() {
	case observation.TermAbsent:
		return "absent", nil
	case observation.TermText:
		text, ok := value.Text()
		if !ok {
			return "", fmt.Errorf("render query text: text term is invalid")
		}
		return "text:" + strconv.Quote(text), nil
	case observation.TermMention:
		mentionID, ok := value.MentionID()
		if !ok {
			return "", fmt.Errorf("render query text: mention term is invalid")
		}
		return "mention:" + mentionID, nil
	case observation.TermEntity:
		entityID, _, ok := value.Entity()
		if !ok {
			return "", fmt.Errorf("render query text: entity term is invalid")
		}
		return "entity:" + entityID, nil
	default:
		return "", fmt.Errorf("render query text: term kind is invalid")
	}
}

func textExtent(value observation.TemporalExtent) (string, error) {
	switch value.Kind() {
	case observation.TemporalUnknown:
		return "unknown", nil
	case observation.TemporalInstant:
		at, ok := value.Instant()
		if !ok {
			return "", fmt.Errorf("render query text: valid-time instant is invalid")
		}
		return "instant:" + queryTime(at), nil
	case observation.TemporalInterval:
		start, hasStart, end, hasEnd := value.Bounds()
		switch {
		case hasStart && hasEnd:
			return fmt.Sprintf("interval:[%s,%s)", queryTime(start), queryTime(end)), nil
		case hasStart:
			return "interval:[" + queryTime(start) + ",)", nil
		case hasEnd:
			return "interval:(," + queryTime(end) + ")", nil
		default:
			return "", fmt.Errorf("render query text: valid-time interval is invalid")
		}
	case observation.TemporalWindow:
		start, hasStart, end, hasEnd := value.Bounds()
		if !hasStart || !hasEnd {
			return "", fmt.Errorf("render query text: valid-time window is invalid")
		}
		return fmt.Sprintf("window:[%s,%s)", queryTime(start), queryTime(end)), nil
	default:
		return "", fmt.Errorf("render query text: valid-time kind is invalid")
	}
}

func entityIDStrings(result query.Result) []string {
	values := make([]string, len(result.EntityIDs))
	for index, value := range result.EntityIDs {
		values[index] = string(value)
	}
	return values
}

func predicateStrings(result query.Result) []string {
	values := make([]string, len(result.Predicates))
	for index, value := range result.Predicates {
		values[index] = string(value)
	}
	return values
}

func textList(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, ", ")
}
