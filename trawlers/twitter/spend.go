package twitter

import (
	"errors"

	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
)

func (r *runtime) runSpend(args []string) (*command.TrawlerCommandResponse, error) {
	if len(args) > 0 {
		return nil, usageErr(errors.New("spend takes no positional arguments"))
	}
	return twitterSpendCommandResponse(r.statusEnvelope().Spend), nil
}
