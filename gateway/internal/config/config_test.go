package config

import "testing"

func TestLoadRequiresDeploymentSecretsAndFixedUpstream(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "postgres://example", "UPSTREAM_URL": "https://api.example.test", "ADMIN_PASSWORD": "secret"}
	get := func(k string) string { return env[k] }
	if _, err := Load(get); err != nil {
		t.Fatal(err)
	}
	delete(env, "ADMIN_PASSWORD")
	if _, err := Load(get); err == nil {
		t.Fatal("admin password is required")
	}
	env["ADMIN_PASSWORD"] = "secret"
	env["UPSTREAM_URL"] = "https://api.example.test/path"
	if _, err := Load(get); err == nil {
		t.Fatal("upstream path would change proxy routing")
	}
}
