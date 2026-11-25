package trawlkit

import (
	"context"
	"strings"
)

type allowedSourceIdentitiesContextKey struct{}

// WithAllowedSourceIdentities attaches a restricted set of registered trawler
// identities to ctx for scoped agent requests so multi-source trawlers such as
// contacts never match or return facts contributed by disallowed sources.
func WithAllowedSourceIdentities(ctx context.Context, allowedSourceIdentities []string) context.Context {
	allowed := make(map[string]struct{}, len(allowedSourceIdentities))
	for _, identity := range allowedSourceIdentities {
		normalized := strings.ToLower(strings.TrimSpace(identity))
		if normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	return context.WithValue(ctx, allowedSourceIdentitiesContextKey{}, allowed)
}

// AllowedSourceIdentitiesFromContext returns the restricted source set for a
// scoped agent request, or (nil, false) when the request is unrestricted.
func AllowedSourceIdentitiesFromContext(ctx context.Context) (map[string]struct{}, bool) {
	if ctx == nil {
		return nil, false
	}
	allowed, ok := ctx.Value(allowedSourceIdentitiesContextKey{}).(map[string]struct{})
	if !ok {
		return nil, false
	}
	return allowed, true
}
