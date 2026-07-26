package config

import "testing"

func TestCoursesEnvironmentNames(t *testing.T) {
	t.Setenv("APP_SERVER_COURSES_CATALOG", "/app/data/catalog.json.gz")
	t.Setenv("APP_SERVER_COURSES_PASSWORD_HASH", "base64-bcrypt")
	t.Setenv("APP_SERVER_COURSES_PASSWORD_HASH_FILE", "/app/data/catalog-password.hash")

	var cfg Config
	if err := LoadEnv("APP", &cfg); err != nil {
		t.Fatalf("load environment: %v", err)
	}

	if got, want := cfg.Server.CoursesCatalog, "/app/data/catalog.json.gz"; got != want {
		t.Fatalf("CoursesCatalog = %q, want %q", got, want)
	}
	if got, want := cfg.Server.CoursesPasswordHash, "base64-bcrypt"; got != want {
		t.Fatalf("CoursesPasswordHash = %q, want %q", got, want)
	}
	if got, want := cfg.Server.CoursesPasswordHashFile, "/app/data/catalog-password.hash"; got != want {
		t.Fatalf("CoursesPasswordHashFile = %q, want %q", got, want)
	}
}
