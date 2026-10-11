package billingpolicy

import "context"

type runContextKey struct{}

// WithRun carries a trusted worker execution ID to every persistence boundary.
func WithRun(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runContextKey{}, runID)
}
func RunID(ctx context.Context) string { id, _ := ctx.Value(runContextKey{}).(string); return id }
