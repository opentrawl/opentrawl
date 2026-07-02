package harness

import "context"

func (s Suite) CheckGrammar(ctx context.Context) CheckResult {
	verbFirst := s.Runner.Run(ctx, "status", "--json")
	if verbFirst.OK() {
		return pass(CheckGrammar, "status --json works")
	}
	flagFirst := s.Runner.Run(ctx, "--json", "status")
	if flagFirst.OK() {
		return fail(CheckGrammar, "crawler accepts --json status but not status --json", "accept the contract grammar: status --json")
	}
	return fail(CheckGrammar, verbFirst.FailureDetail(), "accept verb-first commands with flags after the verb")
}
