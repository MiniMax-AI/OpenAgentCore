package agent

import (
	"errors"
)

var ErrUnknownFunctionCall = errors.New("agent: function call is no longer pending")
