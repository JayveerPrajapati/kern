package main

// UserService is the application service facade. It depends on the repository
// and cache layers via the allowed boundary (cache layer is explicitly allowed).
type UserService struct {
	repo  *UserRepository
	cache *CacheService
	ctx   *TenantContext
}

// NewUserService returns a UserService wired to its dependencies.
func NewUserService(repo *UserRepository, cache *CacheService, ctx *TenantContext) *UserService {
	return &UserService{repo: repo, cache: cache, ctx: ctx}
}

// GetUser returns a user, short-circuiting the repository through the cache.
func (s *UserService) GetUser(id string) (*User, error) {
	key := s.ctx.TenantID() + ":" + id
	if v, ok := s.cache.Get(key); ok {
		if u, ok2 := v.(*User); ok2 {
			return u, nil
		}
	}
	u, err := s.repo.ByID(id)
	if err != nil {
		return nil, err
	}
	s.cache.Set(key, u)
	return u, nil
}