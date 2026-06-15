package main

import (
	"encoding/json"

	"github.com/lightning-rider-999/go-stashapp/internal/redact"
)

// redactAPIKeys scrubs the instance API key out of every pre-signed media URL
// the payload carries. Stash signs Scene.paths.stream and friends with the
// instance API key as an `apikey` query parameter
// (http://host/scene/42/stream?apikey=<JWT>); that JWT travels in the default
// scene payload, so the output layer redacts it before anything is printed or
// logged. The redaction logic lives in [redact.APIKeys] so the conformance
// suite can exercise it directly.
func redactAPIKeys(data json.RawMessage) (json.RawMessage, error) {
	return redact.APIKeys(data)
}
