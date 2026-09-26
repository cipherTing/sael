package config

import (
	"net/url"
	"testing"
)

func TestDatabaseSettingsBuildDSNWithoutDuplicatedPassword(t *testing.T) {
	env := map[string]string{"REDIS_URL": "redis://localhost:6379", "ADMIN_PASSWORD": "admin", "POSTGRES_PASSWORD": "p@ss:#word", "POSTGRES_USER": "operator", "POSTGRES_DB": "sael", "DB_HOST": "db", "DB_PORT": "5432"}
	c, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	if u.Host != "db:5432" || password != "p@ss:#word" || u.User.Username() != "operator" || u.Path != "/sael" {
		t.Fatal("database settings were not encoded correctly")
	}
	env["DATABASE_URL"] = "postgres://external/database"
	c, err = Load(func(k string) string { return env[k] })
	if err != nil || c.DatabaseURL != env["DATABASE_URL"] {
		t.Fatal("explicit external DSN must win")
	}
}

func TestIngressAddressComesFromStartupConfiguration(t *testing.T) {
	env := map[string]string{"REDIS_URL": "redis://localhost:6379", "DATABASE_URL": "postgres://example", "ADMIN_PASSWORD": "secret", "INGRESS_LISTEN_ADDR": ":9081", "ADMIN_LISTEN_ADDR": ":9080"}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil || cfg.Listen != ":9081" {
		t.Fatalf("ingress config: %+v %v", cfg, err)
	}
}

func TestLoadAcceptsInitialUpstreamAndRequiresDeploymentSecrets(t *testing.T) {
	env := map[string]string{"REDIS_URL": "redis://localhost:6379", "DATABASE_URL": "postgres://example", "UPSTREAM_URL": "https://api.example.test", "ADMIN_PASSWORD": "secret"}
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

func TestLoadAllowsAdminToConfigureUpstreamAfterStartup(t *testing.T) {
	env := map[string]string{"REDIS_URL": "redis://localhost:6379", "DATABASE_URL": "postgres://example", "ADMIN_PASSWORD": "secret"}
	got, err := Load(func(k string) string { return env[k] })
	if err != nil || got.Upstream != nil {
		t.Fatalf("config=%+v err=%v", got, err)
	}
}

func TestLoadReadsJevInputLimitDeploymentSettings(t *testing.T) {
	env := map[string]string{"REDIS_URL": "redis://localhost:6379",
		"DATABASE_URL":         "postgres://example",
		"ADMIN_PASSWORD":       "secret",
		"JEV_MAX_INPUT_TOKENS": "120000",
	}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil || cfg.JevMaxInputTokens != 120000 {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
}

func TestLoadRejectsInvalidJevInputLimitSettings(t *testing.T) {
	env := map[string]string{"REDIS_URL": "redis://localhost:6379", "DATABASE_URL": "postgres://example", "ADMIN_PASSWORD": "secret", "JEV_MAX_INPUT_TOKENS": "-1"}
	if _, err := Load(func(k string) string { return env[k] }); err == nil {
		t.Fatal("negative Jev input limit must fail")
	}
}

func TestRedisSettingsEncodePasswordAndRequireConnection(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "postgres://example", "ADMIN_PASSWORD": "secret", "REDIS_PASSWORD": "p@ss:# word", "REDIS_HOST": "cache"}
	c, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := u.User.Password()
	if u.Host != "cache:6379" || p != "p@ss:# word" {
		t.Fatal("Redis credentials not encoded")
	}
	delete(env, "REDIS_PASSWORD")
	if _, err := Load(func(k string) string { return env[k] }); err == nil {
		t.Fatal("Redis must be configured")
	}
}

func TestIngressSafetySettingsRejectInvalidValues(t *testing.T) {
	for key, value := range map[string]string{"MAX_REQUEST_BODY_SIZE": "0", "MAX_HEADER_BYTES": "-1", "READ_HEADER_TIMEOUT": "-1s", "IDLE_TIMEOUT": "oops", "ASYNC_REVIEW_CONCURRENCY": "0"} {
		env := map[string]string{"DATABASE_URL": "postgres://example", "ADMIN_PASSWORD": "secret", "REDIS_URL": "redis://localhost", key: value}
		if _, err := Load(func(k string) string { return env[k] }); err == nil {
			t.Errorf("accepted %s=%s", key, value)
		}
	}
}
