package twitter

import (
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
)

func (r *runtime) runSpend() (*command.TrawlerCommandResponse, error) {
	return twitterSpendCommandResponse(r.statusEnvelope().Spend), nil
}
