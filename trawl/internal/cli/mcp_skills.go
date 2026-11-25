package cli

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"strings"
)

//go:embed skill/personal-wiki/SKILL.md
var canonicalPersonalWikiSkillMarkdown string

const (
	mcpSkillsResultTypeComplete = "complete"
	mcpSkillsCacheTTLMs         = 300000
	mcpSkillsCacheScopePublic   = "public"
	mcpSkillResourceMIMEType    = "text/markdown"
)

type mcpSkillFrontmatter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type mcpSkillResourceDescriptor struct {
	URI    string `json:"uri"`
	Digest string `json:"digest"`
	Size   int    `json:"size"`
}

type mcpSkillDefinition struct {
	URI         string                       `json:"uri"`
	Frontmatter mcpSkillFrontmatter          `json:"frontmatter"`
	Resources   []mcpSkillResourceDescriptor `json:"resources"`
}

type mcpSkillsListResult struct {
	ResultType string               `json:"resultType"`
	Skills     []mcpSkillDefinition `json:"skills"`
	TTLMs      int                  `json:"ttlMs"`
	CacheScope string               `json:"cacheScope"`
}

type mcpSkillsGetResult struct {
	ResultType string             `json:"resultType"`
	Skill      mcpSkillDefinition `json:"skill"`
	TTLMs      int                `json:"ttlMs"`
	CacheScope string             `json:"cacheScope"`
}

type mcpResourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	Size        int    `json:"size,omitempty"`
}

type mcpResourcesListResult struct {
	Resources []mcpResourceDefinition `json:"resources"`
}

type mcpResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
}

type mcpResourcesReadResult struct {
	Contents []mcpResourceContent `json:"contents"`
}

type mcpEmbeddedSkillRecord struct {
	URI         string
	Frontmatter mcpSkillFrontmatter
	RawMarkdown string
	Digest      string
	Size        int
}

func mcpEmbeddedSkills() []mcpEmbeddedSkillRecord {
	sources := []struct {
		uri      string
		markdown string
	}{
		{uri: "skill://opentrawl/SKILL.md", markdown: canonicalOpenTrawlSkillMarkdown},
		{uri: "skill://personal-wiki/SKILL.md", markdown: canonicalPersonalWikiSkillMarkdown},
	}
	records := make([]mcpEmbeddedSkillRecord, 0, len(sources))
	for _, src := range sources {
		frontmatter := parseSkillMarkdownFrontmatter(src.markdown)
		sum := sha256.Sum256([]byte(src.markdown))
		records = append(records, mcpEmbeddedSkillRecord{
			URI:         src.uri,
			Frontmatter: frontmatter,
			RawMarkdown: src.markdown,
			Digest:      "sha256:" + hex.EncodeToString(sum[:]),
			Size:        len(src.markdown),
		})
	}
	return records
}

func parseSkillMarkdownFrontmatter(markdown string) mcpSkillFrontmatter {
	normalized := strings.ReplaceAll(markdown, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return mcpSkillFrontmatter{}
	}
	rest := normalized[len("---\n"):]
	endIdx := strings.Index(rest, "\n---\n")
	if endIdx < 0 {
		return mcpSkillFrontmatter{}
	}
	block := rest[:endIdx]
	var fm mcpSkillFrontmatter
	for _, line := range strings.Split(block, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "name":
			fm.Name = val
		case "description":
			fm.Description = val
		}
	}
	return fm
}

func (record mcpEmbeddedSkillRecord) toSkillDefinition() mcpSkillDefinition {
	return mcpSkillDefinition{
		URI:         record.URI,
		Frontmatter: record.Frontmatter,
		Resources: []mcpSkillResourceDescriptor{
			{
				URI:    record.URI,
				Digest: record.Digest,
				Size:   record.Size,
			},
		},
	}
}

func (record mcpEmbeddedSkillRecord) toResourceDefinition() mcpResourceDefinition {
	return mcpResourceDefinition{
		URI:         record.URI,
		Name:        record.Frontmatter.Name,
		Description: record.Frontmatter.Description,
		MIMEType:    mcpSkillResourceMIMEType,
		Size:        record.Size,
	}
}

func findMCPEmbeddedSkillByURI(uri string) (mcpEmbeddedSkillRecord, bool) {
	trimmed := strings.TrimSpace(uri)
	for _, record := range mcpEmbeddedSkills() {
		if record.URI == trimmed {
			return record, true
		}
	}
	return mcpEmbeddedSkillRecord{}, false
}

func handleMCPSkillsList(requestID json.RawMessage) *mcpJSONRPCResponse {
	records := mcpEmbeddedSkills()
	skills := make([]mcpSkillDefinition, 0, len(records))
	for _, record := range records {
		skills = append(skills, record.toSkillDefinition())
	}
	encoded, err := json.Marshal(mcpSkillsListResult{
		ResultType: mcpSkillsResultTypeComplete,
		Skills:     skills,
		TTLMs:      mcpSkillsCacheTTLMs,
		CacheScope: mcpSkillsCacheScopePublic,
	})
	if err != nil {
		return mcpErrorResponse(requestID, mcpErrorCodeInvalidRequest, "result could not be encoded: "+err.Error())
	}
	return mcpResultResponse(requestID, encoded)
}

func handleMCPSkillsGet(requestID json.RawMessage, params json.RawMessage) *mcpJSONRPCResponse {
	var request struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &request); err != nil || strings.TrimSpace(request.URI) == "" {
		return mcpErrorResponse(requestID, mcpErrorCodeInvalidParameters, "skills/get requires a non-empty uri parameter")
	}
	record, found := findMCPEmbeddedSkillByURI(request.URI)
	if !found {
		return mcpErrorResponse(requestID, mcpErrorCodeInvalidParameters, "unknown skill uri: "+request.URI)
	}
	encoded, err := json.Marshal(mcpSkillsGetResult{
		ResultType: mcpSkillsResultTypeComplete,
		Skill:      record.toSkillDefinition(),
		TTLMs:      mcpSkillsCacheTTLMs,
		CacheScope: mcpSkillsCacheScopePublic,
	})
	if err != nil {
		return mcpErrorResponse(requestID, mcpErrorCodeInvalidRequest, "result could not be encoded: "+err.Error())
	}
	return mcpResultResponse(requestID, encoded)
}

func handleMCPResourcesList(requestID json.RawMessage) *mcpJSONRPCResponse {
	records := mcpEmbeddedSkills()
	resources := make([]mcpResourceDefinition, 0, len(records))
	for _, record := range records {
		resources = append(resources, record.toResourceDefinition())
	}
	encoded, err := json.Marshal(mcpResourcesListResult{Resources: resources})
	if err != nil {
		return mcpErrorResponse(requestID, mcpErrorCodeInvalidRequest, "result could not be encoded: "+err.Error())
	}
	return mcpResultResponse(requestID, encoded)
}

func handleMCPResourcesRead(requestID json.RawMessage, params json.RawMessage) *mcpJSONRPCResponse {
	var request struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &request); err != nil || strings.TrimSpace(request.URI) == "" {
		return mcpErrorResponse(requestID, mcpErrorCodeInvalidParameters, "resources/read requires a non-empty uri parameter")
	}
	record, found := findMCPEmbeddedSkillByURI(request.URI)
	if !found {
		return mcpErrorResponse(requestID, mcpErrorCodeInvalidParameters, "unknown resource uri: "+request.URI)
	}
	encoded, err := json.Marshal(mcpResourcesReadResult{
		Contents: []mcpResourceContent{
			{
				URI:      record.URI,
				MIMEType: mcpSkillResourceMIMEType,
				Text:     record.RawMarkdown,
			},
		},
	})
	if err != nil {
		return mcpErrorResponse(requestID, mcpErrorCodeInvalidRequest, "result could not be encoded: "+err.Error())
	}
	return mcpResultResponse(requestID, encoded)
}
