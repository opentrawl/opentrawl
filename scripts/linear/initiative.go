package main

import (
	"context"
	"fmt"
	"strings"
)

type InitiativeUpdateOptions struct {
	Summary     *string
	Description *string
}

func (api *LinearAPI) GetInitiative(ctx context.Context, reference string) (Initiative, error) {
	initiative, err := api.ResolveInitiative(ctx, reference)
	if err != nil {
		return Initiative{}, err
	}
	return api.GetInitiativeByID(ctx, initiative.ID)
}

func (api *LinearAPI) GetInitiativeByID(ctx context.Context, id string) (Initiative, error) {
	readProjects, after := true, ""
	var initiative Initiative
	for page := 0; readProjects; page++ {
		var out struct {
			Initiative *Initiative `json:"initiative"`
		}
		variables := map[string]any{"id": id, "readProjects": readProjects}
		if after != "" {
			variables["projectsAfter"] = after
		}
		if err := api.graph.Do(ctx, initiativeByIDQuery, variables, &out); err != nil {
			return Initiative{}, err
		}
		if out.Initiative == nil {
			return Initiative{}, fmt.Errorf("initiative %q was not found", id)
		}
		pageInitiative := *out.Initiative
		if page == 0 {
			initiative = pageInitiative
		} else {
			initiative.Projects.Nodes = append(initiative.Projects.Nodes, pageInitiative.Projects.Nodes...)
			initiative.Projects.PageInfo = pageInitiative.Projects.PageInfo
		}
		var err error
		readProjects, after, err = nextProjectPage(pageInitiative.Projects.PageInfo, "initiative project")
		if err != nil {
			return Initiative{}, err
		}
	}
	return initiative, nil
}

func (api *LinearAPI) UpdateInitiative(ctx context.Context, reference, actor string, options InitiativeUpdateOptions) (Initiative, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return Initiative{}, fmt.Errorf("--as is required for write commands")
	}
	if options.Summary == nil && options.Description == nil {
		return Initiative{}, fmt.Errorf("at least one initiative field is required")
	}
	initiative, err := api.ResolveInitiative(ctx, reference)
	if err != nil {
		return Initiative{}, err
	}
	input := map[string]any{}
	if options.Summary != nil {
		summary := *options.Summary
		if strings.EqualFold(strings.TrimSpace(summary), "none") {
			summary = ""
		}
		input["description"] = summary
	}
	if options.Description != nil {
		input["content"] = *options.Description
	}
	if api.logger != nil {
		api.logger.LogDiagnostic("info", fmt.Sprintf("initiativeUpdate requested: %s by %s", initiative.Name, actor))
	}
	var out struct {
		InitiativeUpdate struct {
			Success bool `json:"success"`
		} `json:"initiativeUpdate"`
	}
	if err := api.graph.Do(ctx, updateInitiativeMutation, map[string]any{"id": initiative.ID, "input": input}, &out); err != nil {
		return Initiative{}, err
	}
	if !out.InitiativeUpdate.Success {
		return Initiative{}, fmt.Errorf("linear did not update the initiative")
	}
	readBack, err := api.GetInitiativeByID(ctx, initiative.ID)
	if err != nil {
		return Initiative{}, initiativeFieldsChangedError(initiative, err)
	}
	if options.Summary != nil && readBack.Description != input["description"] {
		return Initiative{}, initiativeFieldsChangedError(readBack, fmt.Errorf("initiative read-back summary did not match the requested value"))
	}
	if options.Description != nil && readBack.Content != *options.Description {
		return Initiative{}, initiativeFieldsChangedError(readBack, fmt.Errorf("initiative read-back description did not match the requested value"))
	}
	return readBack, nil
}

func initiativeFieldsChangedError(initiative Initiative, cause error) error {
	return fmt.Errorf("initiative %q (%s) fields may already have changed, but verification failed: %w", initiative.Name, initiative.ID, cause)
}
