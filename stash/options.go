package stash

import (
	"log/slog"
	"net/http"
	"time"
)

// config holds the resolved settings used to build a [Client]. It is populated
// by the [Option] functions passed to [NewClient] and then merged with the
// environment.
type config struct {
	// rawURL is the URL as supplied by an option, before normalisation. Empty
	// means "fall back to the STASHAPP_URL environment variable".
	rawURL string
	// apiKey is the credential sent in the ApiKey header. Empty is valid: some
	// instances run unauthenticated, in which case no header is sent.
	apiKey string
	// apiKeySet records whether WithAPIKey was called, so an explicit empty key
	// can suppress the environment fallback.
	apiKeySet bool
	// httpClient, when non-nil, is the caller-supplied transport. Its own
	// Timeout is preserved; only its RoundTripper is wrapped to inject the key.
	httpClient *http.Client
	// timeout is applied to the default *http.Client when no httpClient is
	// supplied. Zero means use the package default.
	timeout time.Duration
	// logger is the diagnostics logger. Nil means use slog.Default.
	logger *slog.Logger
}

// Option configures a [Client] built by [NewClient].
type Option func(*config)

// WithURL sets the Stash base URL. It overrides the STASHAPP_URL environment
// variable. The URL is normalised so that its path points at the GraphQL
// endpoint: a bare host or a root path gains a /graphql suffix, while a URL
// that already targets /graphql is left as is.
func WithURL(rawURL string) Option {
	return func(c *config) { c.rawURL = rawURL }
}

// WithAPIKey sets the credential sent in the ApiKey request header. It
// overrides the STASHAPP_API_KEY environment variable. An empty key is valid
// and suppresses the header entirely, which suits unauthenticated instances.
func WithAPIKey(key string) Option {
	return func(c *config) {
		c.apiKey = key
		c.apiKeySet = true
	}
}

// WithHTTPClient supplies the transport used for requests. The client's own
// Timeout and settings are kept; only its RoundTripper is wrapped so the
// ApiKey header is still injected. When this option is used, WithTimeout has no
// effect.
func WithHTTPClient(h *http.Client) Option {
	return func(c *config) { c.httpClient = h }
}

// WithTimeout sets the timeout on the default *http.Client. It is ignored when
// WithHTTPClient supplies a client, whose own timeout is then authoritative.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithLogger sets the slog.Logger used for diagnostics. A nil logger falls back
// to slog.Default, so [Client.Logger] never returns nil.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) { c.logger = l }
}
