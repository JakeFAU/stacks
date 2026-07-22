package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEntitiesCommandShowsCanonicalEntityAliasesAndEvidence(t *testing.T) {
	store := &fakeEntityStore{entity: EntityView{
		ID:          "person-1",
		DisplayName: "Synthetic Person",
		RecordedAt:  time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
		Aliases:     []string{"synthetic.person@example.test"},
		Evidence:    []string{"Synthetic evidence context"},
	}}
	var stdout strings.Builder
	command := EntitiesCommand{Service: &EntityService{Store: store}, Output: &stdout}

	if err := command.Run(context.Background(), []string{"show", "person-1"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, value := range []string{"person-1", "Synthetic Person", "2026-07-21T12:00:00Z", "synthetic.person@example.test", "Synthetic evidence context"} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), value)
		}
	}
}

type fakeEntityStore struct{ entity EntityView }

func (store *fakeEntityStore) ListEntities(context.Context) ([]EntityView, error) {
	return []EntityView{store.entity}, nil
}

func (store *fakeEntityStore) ShowEntity(context.Context, string) (EntityView, error) {
	return store.entity, nil
}
