package main

type Project struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	SlugID        string        `json:"slugId"`
	Description   string        `json:"description"`
	Content       string        `json:"content"`
	Status        ProjectStatus `json:"status"`
	Priority      int           `json:"priority"`
	PriorityLabel string        `json:"priorityLabel"`
	Health        string        `json:"health"`
	Lead          *Person       `json:"lead"`
	Teams         struct {
		Nodes []Team `json:"nodes"`
	} `json:"teams"`
	Milestones struct {
		Nodes    []ProjectMilestone `json:"nodes"`
		PageInfo PageInfo           `json:"pageInfo"`
	} `json:"projectMilestones"`
	Issues struct {
		Nodes    []Issue  `json:"nodes"`
		PageInfo PageInfo `json:"pageInfo"`
	} `json:"issues"`
	Initiatives struct {
		Nodes    []Initiative `json:"nodes"`
		PageInfo PageInfo     `json:"pageInfo"`
	} `json:"initiatives"`
}

type Initiative struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SlugID      string `json:"slugId"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Projects    struct {
		Nodes    []Project `json:"nodes"`
		PageInfo PageInfo  `json:"pageInfo"`
	} `json:"projects"`
}

type ProjectStatus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
