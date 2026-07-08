package birdcrawl

import "errors"

func (r *runtime) runSpend(args []string) error {
	if len(args) > 0 {
		return usageErr(errors.New("spend takes no positional arguments"))
	}
	return r.print(r.statusEnvelope().Spend)
}
