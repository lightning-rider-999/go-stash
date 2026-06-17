package main

import (
	"encoding/json"

	"github.com/lightning-rider-999/go-stash/internal/redact"
)

// redactAPIKeys scrubs the instance credential out of a payload before the
// output layer prints or logs it. Two leaks are covered: pre-signed media URLs
// that carry the API key as an `apikey` query parameter (Scene.paths.stream and
// friends), and bare secret config fields (apiKey, password, generateAPIKey,
// username) that the Configuration query and generateAPIKey mutation return as
// plain strings. The logic lives in [redact.APIKeys] so the conformance suite
// can exercise it directly.
func redactAPIKeys(data json.RawMessage) (json.RawMessage, error) {
	return redact.APIKeys(data)
}
