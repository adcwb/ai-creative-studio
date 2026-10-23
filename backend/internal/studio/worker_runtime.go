package studio

import "errors"

// ConfigureWorkerRuntimeFromEnv installs authorization/model records without
// exposing HTTP or starting OIDC. KEK is deployment-injected and never logged.
func ConfigureWorkerRuntimeFromEnv(s *FileStore, dir string) error {
	if s == nil || s.pg == nil {
		return errors.New("worker runtime requires postgres")
	}
	auth, err := OpenPostgresAuthStore(dir, s.pg.db)
	if err != nil {
		return err
	}
	key, err := modelKeyFromEnv()
	if err != nil {
		return err
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	auth.models, err = OpenPostgresModelConfigStore(dir, s.pg.db, key)
	if err != nil {
		return err
	}
	ConfigureManagedRuntime(s, auth)
	return nil
}
