package cli

// SkillCmd prints the embedded canonical OpenTrawl SKILL.md document so agents
// and humans can inspect or install the current instructions directly from the
// binary without cloning the repository.
type SkillCmd struct{}

func (c *SkillCmd) Run(r *Runtime) error {
	return r.output.WriteBundledAgentSkillMarkdown(canonicalOpenTrawlSkillMarkdown)
}
