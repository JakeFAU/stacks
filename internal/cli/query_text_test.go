package cli

import (
	"bytes"
	"testing"
)

func TestQueryTextShowsExactCompleteTrendAssociations(t *testing.T) {
	rendered, err := renderQueryText(populatedTrendResult(t, false))
	if err != nil {
		t.Fatalf("renderQueryText() error = %v", err)
	}
	const want = `intent: trend-comparison
entities: entity-a, entity-b
entity match: all
predicates: a.changed, b.removed, c.added, d.conflict, e.hypothesis
before window: [2026-01-01T13:09:10.654321Z, 2026-02-01T13:09:10.654321Z)
after window: [2026-03-01T13:09:10.654321Z, 2026-04-01T13:09:10.654321Z)
knowledge scope: as-of 2026-04-30T23:02:03.987654Z
limit: 0
before facts:
  - key: subject=text:"subject bytes" predicate=b.removed value=entity:entity-owner
    contributions:
      - observation_id=observation-removed status=hypothesized valid_time=interval:[2025-12-01T13:09:10.654321Z,) recorded_at=2026-06-01T09:06:07.123456Z derivation_method=extract derivation_version=v1
    supporting citations:
      - evidence_id=evidence-removed role=supporting source_document_id=document-evidence-removed document_version_id=version-evidence-removed section_id=section-evidence-removed section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
    contradicting citations:
      (none)
  - key: subject=entity:entity-a predicate=a.changed value=text:"remote"
    contributions:
      - observation_id=observation-changed-a status=observed valid_time=unknown recorded_at=2026-06-01T09:06:07.123456Z derivation_method=manual derivation_version=v1
      - observation_id=observation-changed-b status=inferred valid_time=instant:2026-01-16T13:09:10.654321Z recorded_at=2026-06-01T09:06:07.123456Z derivation_method=extract derivation_version=v2 run_id=run-2 model=synthetic-model prompt_version=prompt-v3 subject_grounding_mention_id=mention-subject object_grounding_mention_id=mention-object
    supporting citations:
      - evidence_id=evidence-support-a role=supporting source_document_id=document-evidence-support-a document_version_id=version-evidence-support-a section_id=section-evidence-support-a section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
      - evidence_id=evidence-support-b role=supporting source_document_id=document-evidence-support-b document_version_id=version-evidence-support-b section_id=section-evidence-support-b section_title="Synthetic section" section_path=["Parent" "Child"] section_order=2 section_role=body offsets=3:11 locator="synthetic://document/evidence-support-b" text="exact synthetic bytes"
    contradicting citations:
      - evidence_id=evidence-counter-a role=contradicting source_document_id=document-evidence-counter-a document_version_id=version-evidence-counter-a section_id=section-evidence-counter-a section_title="Synthetic section" section_path=["Parent" "Child"] section_order=2 section_role=body offsets=3:11 locator="synthetic://document/evidence-counter-a" text="exact synthetic bytes"
before unresolved:
  - key: subject=entity:entity-b predicate=d.conflict reason=conflicting-values
    candidates:
      - key: subject=entity:entity-b predicate=d.conflict value=text:"alpha"
        contributions:
          - observation_id=observation-conflict-a status=rejected valid_time=window:[2026-01-10T13:09:10.654321Z,2026-01-20T13:09:10.654321Z) recorded_at=2026-06-01T09:06:07.123456Z derivation_method=review derivation_version=v2
        supporting citations:
          - evidence_id=evidence-conflict-a role=supporting source_document_id=document-evidence-conflict-a document_version_id=version-evidence-conflict-a section_id=section-evidence-conflict-a section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
        contradicting citations:
          (none)
      - key: subject=entity:entity-b predicate=d.conflict value=text:"beta"
        contributions:
          - observation_id=observation-conflict-b status=validated_empirically valid_time=interval:[2026-01-11T13:09:10.654321Z,2026-01-21T13:09:10.654321Z) recorded_at=2026-06-01T09:06:07.123456Z derivation_method=review derivation_version=v2
        supporting citations:
          - evidence_id=evidence-conflict-b role=supporting source_document_id=document-evidence-conflict-b document_version_id=version-evidence-conflict-b section_id=section-evidence-conflict-b section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
        contradicting citations:
          (none)
after facts:
  - key: subject=absent predicate=c.added value=absent
    contributions:
      - observation_id=observation-added status=validated_structurally valid_time=interval:(,2026-04-01T13:09:10.654321Z) recorded_at=2026-06-01T09:06:07.123456Z derivation_method=rule derivation_version=v5
    supporting citations:
      - evidence_id=evidence-added role=supporting source_document_id=document-evidence-added document_version_id=version-evidence-added section_id=section-evidence-added section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
    contradicting citations:
      (none)
  - key: subject=entity:entity-a predicate=a.changed value=text:"office"
    contributions:
      - observation_id=observation-after status=validated_empirically valid_time=interval:[2026-03-01T13:09:10.654321Z,2026-04-01T13:09:10.654321Z) recorded_at=2026-06-01T09:06:07.123456Z derivation_method=review derivation_version=v4
    supporting citations:
      - evidence_id=evidence-after role=supporting source_document_id=document-evidence-after document_version_id=version-evidence-after section_id=section-evidence-after section_title="Synthetic section" section_path=["Parent" "Child"] section_order=2 section_role=body offsets=3:11 locator="synthetic://document/evidence-after" text="exact synthetic bytes"
    contradicting citations:
      (none)
after unresolved:
  - key: subject=text:"hypothesis subject" predicate=e.hypothesis reason=hypothesized
    candidates:
      - key: subject=text:"hypothesis subject" predicate=e.hypothesis value=text:"candidate"
        contributions:
          - observation_id=observation-hypothesis status=hypothesized valid_time=instant:2026-03-12T13:09:10.654321Z recorded_at=2026-06-01T09:06:07.123456Z derivation_method=extract derivation_version=v7
        supporting citations:
          (none)
        contradicting citations:
          - evidence_id=evidence-hypothesis role=contradicting source_document_id=document-evidence-hypothesis document_version_id=version-evidence-hypothesis section_id=section-evidence-hypothesis section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
changes:
  - kind=added key: subject=absent predicate=c.added
    after:
      - key: subject=absent predicate=c.added value=absent
        contributions:
          - observation_id=observation-added status=validated_structurally valid_time=interval:(,2026-04-01T13:09:10.654321Z) recorded_at=2026-06-01T09:06:07.123456Z derivation_method=rule derivation_version=v5
        supporting citations:
          - evidence_id=evidence-added role=supporting source_document_id=document-evidence-added document_version_id=version-evidence-added section_id=section-evidence-added section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
        contradicting citations:
          (none)
  - kind=removed key: subject=text:"subject bytes" predicate=b.removed
    before:
      - key: subject=text:"subject bytes" predicate=b.removed value=entity:entity-owner
        contributions:
          - observation_id=observation-removed status=hypothesized valid_time=interval:[2025-12-01T13:09:10.654321Z,) recorded_at=2026-06-01T09:06:07.123456Z derivation_method=extract derivation_version=v1
        supporting citations:
          - evidence_id=evidence-removed role=supporting source_document_id=document-evidence-removed document_version_id=version-evidence-removed section_id=section-evidence-removed section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
        contradicting citations:
          (none)
  - kind=changed key: subject=entity:entity-a predicate=a.changed
    before:
      - key: subject=entity:entity-a predicate=a.changed value=text:"remote"
        contributions:
          - observation_id=observation-changed-a status=observed valid_time=unknown recorded_at=2026-06-01T09:06:07.123456Z derivation_method=manual derivation_version=v1
          - observation_id=observation-changed-b status=inferred valid_time=instant:2026-01-16T13:09:10.654321Z recorded_at=2026-06-01T09:06:07.123456Z derivation_method=extract derivation_version=v2 run_id=run-2 model=synthetic-model prompt_version=prompt-v3 subject_grounding_mention_id=mention-subject object_grounding_mention_id=mention-object
        supporting citations:
          - evidence_id=evidence-support-a role=supporting source_document_id=document-evidence-support-a document_version_id=version-evidence-support-a section_id=section-evidence-support-a section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
          - evidence_id=evidence-support-b role=supporting source_document_id=document-evidence-support-b document_version_id=version-evidence-support-b section_id=section-evidence-support-b section_title="Synthetic section" section_path=["Parent" "Child"] section_order=2 section_role=body offsets=3:11 locator="synthetic://document/evidence-support-b" text="exact synthetic bytes"
        contradicting citations:
          - evidence_id=evidence-counter-a role=contradicting source_document_id=document-evidence-counter-a document_version_id=version-evidence-counter-a section_id=section-evidence-counter-a section_title="Synthetic section" section_path=["Parent" "Child"] section_order=2 section_role=body offsets=3:11 locator="synthetic://document/evidence-counter-a" text="exact synthetic bytes"
    after:
      - key: subject=entity:entity-a predicate=a.changed value=text:"office"
        contributions:
          - observation_id=observation-after status=validated_empirically valid_time=interval:[2026-03-01T13:09:10.654321Z,2026-04-01T13:09:10.654321Z) recorded_at=2026-06-01T09:06:07.123456Z derivation_method=review derivation_version=v4
        supporting citations:
          - evidence_id=evidence-after role=supporting source_document_id=document-evidence-after document_version_id=version-evidence-after section_id=section-evidence-after section_title="Synthetic section" section_path=["Parent" "Child"] section_order=2 section_role=body offsets=3:11 locator="synthetic://document/evidence-after" text="exact synthetic bytes"
        contradicting citations:
          (none)
unresolved keys:
  - subject=text:"hypothesis subject" predicate=e.hypothesis
  - subject=entity:entity-b predicate=d.conflict
gaps:
  - kind=no-evidence
  - kind=valid-time-excluded entity_id=entity-b predicate=b.removed selection_label=after
`
	if string(rendered) != want {
		t.Fatalf("renderQueryText() mismatch\n--- got ---\n%s--- want ---\n%s", rendered, want)
	}
}

func TestQueryTextIsDeterministicAcrossReorderedCanonicalInputs(t *testing.T) {
	first, err := renderQueryText(populatedTrendResult(t, false))
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderQueryText(populatedTrendResult(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("reordered result bytes differ:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
