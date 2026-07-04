package harness

import "context"

type MetadataInfo struct {
	Capabilities []string
	AppID        string
	Valid        bool
}

type StatusInfo struct {
	Value map[string]any
	Valid bool
}

type Suite struct {
	Runner Runner
}

func Run(ctx context.Context, binary string) Report {
	return Suite{Runner: NewRunner(binary)}.Run(ctx)
}

func (s Suite) Run(ctx context.Context) Report {
	metadataResult, metadata := s.CheckMetadata(ctx)
	statusResult, status := s.CheckStatus(ctx)
	return Report{
		metadataResult,
		s.CheckGrammar(ctx),
		statusResult,
		s.CheckDoctor(ctx),
		s.CheckSecrets(ctx),
		s.CheckReadsNeverMutate(ctx, status),
		s.CheckSearch(ctx, metadata, status),
		s.CheckOpen(ctx, metadata, status),
		s.CheckHumanReadable(ctx),
	}
}
