package store

import "context"

func (s *Store) SaveReadonlyCredential(ctx context.Context, credential AdminCredential) error {
	return s.Settings.SaveReadonlyCredential(ctx, credential)
}

func (s *Store) LoadReadonlyCredential(ctx context.Context) (AdminCredential, bool, error) {
	return s.Settings.LoadReadonlyCredential(ctx)
}
