package auth

import "context"

// Memory composition resolves its live credit pool without coupling identity
// stores to credit's administration dependencies. PG retains its one-statement
// identity/member/plan/source snapshot.
func (s *Service) SetMemoryPlanResolver(resolve func(context.Context, string) (string, string, error)) {
	s.memoryPlanResolver = resolve
}
