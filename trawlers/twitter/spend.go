package twitter

import (
	"errors"

	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
)

func (r *runtime) runSpend(args []string) (*commandv1.TrawlerCommandResponse, error) {
	if len(args) > 0 {
		return nil, usageErr(errors.New("spend takes no positional arguments"))
	}
	return twitterSpendCommandResponse(r.statusEnvelope().Spend), nil
}
