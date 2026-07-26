package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// EntityView is the private review projection of one canonical entity.
type EntityView struct {
	ID           string
	DisplayName  string
	RecordedAt   time.Time
	Aliases      []string
	MentionCount int
	Evidence     []ReviewEvidence
}

// EntityStore provides entity projections at the CLI boundary.
type EntityStore interface {
	ListEntities(ctx context.Context) ([]EntityView, error)
	ShowEntity(ctx context.Context, entityID string) (EntityView, error)
}

// EntityService owns entity-review actions without logging private context.
type EntityService struct {
	Store EntityStore
}

// List returns canonical people suitable for private local review.
func (service EntityService) List(ctx context.Context) ([]EntityView, error) {
	if service.Store == nil {
		return nil, fmt.Errorf("list entities: store is not configured")
	}
	return service.Store.ListEntities(ctx)
}

// Show returns one canonical person and its cited evidence.
func (service EntityService) Show(ctx context.Context, entityID string) (EntityView, error) {
	if strings.TrimSpace(entityID) == "" {
		return EntityView{}, fmt.Errorf("show entity: entity ID is required")
	}
	if service.Store == nil {
		return EntityView{}, fmt.Errorf("show entity: store is not configured")
	}
	return service.Store.ShowEntity(ctx, entityID)
}

// EntitiesCommand renders private entity-review data to its explicit stdout
// boundary. It intentionally does not log aliases or evidence.
type EntitiesCommand struct {
	Service *EntityService
	Output  io.Writer
}

// Run executes `entities list` or `entities show <entity-id>`.
func (command EntitiesCommand) Run(ctx context.Context, args []string) error {
	if command.Service == nil {
		return fmt.Errorf("entities command: service is not configured")
	}
	output := command.Output
	if output == nil {
		output = io.Discard
	}
	if len(args) == 1 && args[0] == "list" {
		entities, err := command.Service.List(ctx)
		if err != nil {
			return err
		}
		for _, entity := range entities {
			fmt.Fprintf(output, "%s\t%s\trecorded=%s\tmentions=%d\n", entity.ID, entity.DisplayName, entity.RecordedAt.UTC().Format(time.RFC3339), entity.MentionCount)
		}
		return nil
	}
	if len(args) == 2 && args[0] == "show" {
		entity, err := command.Service.Show(ctx, args[1])
		if err != nil {
			return err
		}
		renderEntity(output, entity)
		return nil
	}
	return fmt.Errorf("entities command usage: entities list | entities show <entity-id>")
}

func renderEntity(output io.Writer, entity EntityView) {
	fmt.Fprintf(output, "entity %s\nname: %s\nrecorded: %s\nmentions: %d\n", entity.ID, entity.DisplayName, entity.RecordedAt.UTC().Format(time.RFC3339), entity.MentionCount)
	for _, alias := range entity.Aliases {
		fmt.Fprintf(output, "alias: %s\n", alias)
	}
	for _, evidence := range entity.Evidence {
		fmt.Fprintf(output, "evidence %s: %s\n", evidence.ID, evidence.Quote)
	}
}
