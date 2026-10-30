// Registry resolves the correct Provider for a given user.
// Priority: user's active BYOK key -> platform default keys.
// To add a new provider (OpenAI, Anthropic, AWS), implement Provider in a new
// file and add a case to newProviderForKey below.

package ai

import (
	"fmt"
	"os"
	"strings"
)

// Registry holds the platform-default provider and resolves per-user overrides.
type Registry struct {
	defaultProvider Provider
}

// NewRegistry creates a Registry backed by the platform Gemini keys from env.
func NewRegistry() *Registry {
	return &Registry{
		defaultProvider: NewGeminiProvider(platformKeys()),
	}
}

// ForUser returns the Provider to use for a given user.
// If the user has an active BYOK key, it is tried first with the platform keys
// as fallback. If no user key is set, the platform default is returned.
func (r *Registry) ForUser(userKey, providerName string) Provider {
	if userKey == "" {
		return r.defaultProvider
	}

	switch strings.ToLower(providerName) {
	case "gemini", "":
		// User key first, then platform fallback keys
		keys := append([]string{userKey}, platformKeys()...)
		return NewGeminiProvider(keys)
	default:
		// Unknown provider — fall back to platform default
		return r.defaultProvider
	}
}

// platformKeys reads the comma-separated GEMINI_API_KEYS env var.
func platformKeys() []string {
	raw := os.Getenv("GEMINI_API_KEYS")
	if raw == "" {
		raw = os.Getenv("GEMINI_API_KEY")
	}
	var keys []string
	for _, k := range strings.Split(raw, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

// ErrNoProvider is returned when no provider can be resolved.
var ErrNoProvider = fmt.Errorf("no AI provider configured")
