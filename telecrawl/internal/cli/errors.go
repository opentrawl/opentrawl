package cli

import (
	"errors"

	cklog "github.com/openclaw/crawlkit/log"
)

// usageRemedy is the one next-step hint for every caller mistake. It rides the
// error body's remedy field, kept out of the message (rules §2.4).
const usageRemedy = "Run 'telecrawl --help'."

// usageErr marks a caller mistake: exit 2, and rejected (not logged as crawler
// health) via isUsageError.
func usageErr(err error) error {
	return &cliError{code: 2, name: "usage", message: err.Error(), remedy: usageRemedy, err: err}
}

// usageErrHelp is a usage error whose text-mode output also shows full command
// usage; the JSON body and the log keep the short message.
func usageErrHelp(message, help string) error {
	return &cliError{
		code:    2,
		name:    "usage",
		message: message,
		remedy:  usageRemedy,
		human:   message + "\n\n" + help,
		err:     errors.New(message),
	}
}

// contractError is a command failure with a machine code, human message and
// remedy. WriteJSONErrorIfNeeded renders it as the {"error": {...}} envelope.
func (r *runtime) contractError(code, message, remedy string) error {
	return &cliError{
		code:    1,
		name:    code,
		message: message,
		remedy:  remedy,
		err:     cklog.WorldMustChange{Err: errors.New(message), Message: message, Remedy: remedy},
	}
}

func isUsageError(err error) bool {
	var cli *cliError
	return errors.As(err, &cli) && cli.name == "usage"
}

// loggableError keeps the health log clean: it records a command failure's
// short machine message, never the rendered human table a who error carries.
func loggableError(err error) error {
	var codeErr *cliError
	if errors.As(err, &codeErr) && codeErr.message != "" {
		return errors.New(codeErr.message)
	}
	return err
}
