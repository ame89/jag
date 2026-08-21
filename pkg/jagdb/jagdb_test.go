package jagdb

import "testing"

func TestParseSQLiteFile(t *testing.T) {
	backend, conn, err := Parse("sqlite://foo.db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend != SQLite {
		t.Fatalf("expected SQLite, got %v", backend)
	}
	if conn != "foo.db" {
		t.Fatalf("expected foo.db, got %q", conn)
	}
}

func TestParseSQLiteMemory(t *testing.T) {
	backend, conn, err := Parse("sqlite://:memory")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend != SQLite {
		t.Fatalf("expected SQLite, got %v", backend)
	}
	if conn != ":memory:" {
		t.Fatalf("expected :memory:, got %q", conn)
	}
}

func TestParsePostgres(t *testing.T) {
	dsn := "postgres://jag:jag@localhost:5432/jag?sslmode=disable"
	backend, conn, err := Parse(dsn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend != Postgres {
		t.Fatalf("expected Postgres, got %v", backend)
	}
	if conn != dsn {
		t.Fatalf("expected dsn unchanged, got %q", conn)
	}
}

func TestParsePostgresAltScheme(t *testing.T) {
	dsn := "postgresql://jag:jag@localhost:5432/jag?sslmode=disable"
	backend, _, err := Parse(dsn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend != Postgres {
		t.Fatalf("expected Postgres, got %v", backend)
	}
}

func TestParseUnrecognizedPrefix(t *testing.T) {
	if _, _, err := Parse("mysql://foo"); err == nil {
		t.Fatal("expected error for unrecognized prefix")
	}
	if _, _, err := Parse("foo.db"); err == nil {
		t.Fatal("expected error for missing prefix")
	}
	if _, _, err := Parse(""); err == nil {
		t.Fatal("expected error for empty value")
	}
}

func TestFromEnvUnset(t *testing.T) {
	t.Setenv("JAG_DATABASE", "")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("expected error when JAG_DATABASE is unset")
	}
}

func TestFromEnvSet(t *testing.T) {
	t.Setenv("JAG_DATABASE", "sqlite://bar.db")
	backend, conn, err := FromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend != SQLite || conn != "bar.db" {
		t.Fatalf("expected sqlite/bar.db, got %v/%q", backend, conn)
	}
}
