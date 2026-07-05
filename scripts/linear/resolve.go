package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func (api *LinearAPI) ResolveTeam(ctx context.Context, key string) (Team, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "" {
		return Team{}, fmt.Errorf("--team is required")
	}
	var out struct {
		Teams struct {
			Nodes []Team `json:"nodes"`
		} `json:"teams"`
	}
	if err := api.graph.Do(ctx, resolveTeamQuery, map[string]any{"key": key}, &out); err != nil {
		return Team{}, err
	}
	if len(out.Teams.Nodes) == 0 {
		return Team{}, fmt.Errorf("team %s was not found", key)
	}
	if len(out.Teams.Nodes) > 1 {
		return Team{}, fmt.Errorf("team %s matched more than one Linear team", key)
	}
	return out.Teams.Nodes[0], nil
}

func (api *LinearAPI) ResolveLabels(ctx context.Context, team string, names []string) ([]string, error) {
	names = cleanLabelNames(names)
	if len(names) == 0 {
		return nil, nil
	}
	var out struct {
		IssueLabels struct {
			Nodes    []IssueLabel `json:"nodes"`
			PageInfo PageInfo     `json:"pageInfo"`
		} `json:"issueLabels"`
	}
	if err := api.graph.Do(ctx, resolveLabelsQuery, map[string]any{"names": names}, &out); err != nil {
		return nil, err
	}
	if out.IssueLabels.PageInfo.HasNextPage {
		return nil, fmt.Errorf("Linear returned more than 100 matching labels. Narrow the label names and try again")
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		matches := matchingLabels(out.IssueLabels.Nodes, team, name)
		if len(matches) == 0 {
			return nil, fmt.Errorf("label %q was not found for team %s", name, team)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("label %q is ambiguous: %s", name, labelChoices(matches))
		}
		if matches[0].IsGroup {
			return nil, fmt.Errorf("label %q is a group and cannot be applied", name)
		}
		ids = append(ids, matches[0].ID)
	}
	return ids, nil
}

func (api *LinearAPI) ResolveStateName(ctx context.Context, team, state string) (string, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return "", fmt.Errorf("--state needs a value")
	}
	states, err := api.TeamStates(ctx, team)
	if err != nil {
		return "", err
	}
	var matches []IssueState
	for _, candidate := range states {
		if strings.EqualFold(candidate.Name, state) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 1 {
		return matches[0].Name, nil
	}
	valid := stateNames(states)
	if len(matches) > 1 {
		return "", fmt.Errorf("state %q is ambiguous for team %s. Valid states: %s", state, team, strings.Join(valid, ", "))
	}
	if len(valid) == 0 {
		return "", fmt.Errorf("team %s has no Linear states", team)
	}
	return "", fmt.Errorf("state %q was not found for team %s. Valid states: %s", state, team, strings.Join(valid, ", "))
}

func (api *LinearAPI) TeamStates(ctx context.Context, team string) ([]IssueState, error) {
	team = strings.ToUpper(strings.TrimSpace(team))
	if team == "" {
		return nil, fmt.Errorf("--team is required")
	}
	var out struct {
		WorkflowStates struct {
			Nodes    []IssueState `json:"nodes"`
			PageInfo PageInfo     `json:"pageInfo"`
		} `json:"workflowStates"`
	}
	if err := api.graph.Do(ctx, teamStatesQuery, map[string]any{"team": team}, &out); err != nil {
		return nil, err
	}
	if out.WorkflowStates.PageInfo.HasNextPage {
		return nil, fmt.Errorf("Linear returned more than 100 states for team %s. Narrow the state name and try again", team)
	}
	return out.WorkflowStates.Nodes, nil
}

func cleanLabelNames(names []string) []string {
	seen := map[string]bool{}
	var cleaned []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		cleaned = append(cleaned, name)
	}
	return cleaned
}

func matchingLabels(labels []IssueLabel, team, name string) []IssueLabel {
	var matches []IssueLabel
	for _, label := range labels {
		if label.Name != name {
			continue
		}
		if label.Team == nil || strings.EqualFold(label.Team.Key, team) {
			matches = append(matches, label)
		}
	}
	return matches
}

func labelChoices(labels []IssueLabel) string {
	choices := make([]string, 0, len(labels))
	for _, label := range labels {
		scope := "workspace"
		if label.Team != nil && strings.TrimSpace(label.Team.Key) != "" {
			scope = "team " + label.Team.Key
		}
		choices = append(choices, scope)
	}
	return strings.Join(choices, ", ")
}

func stateNames(states []IssueState) []string {
	names := make([]string, 0, len(states))
	seen := map[string]bool{}
	for _, state := range states {
		name := strings.TrimSpace(state.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
