package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/lib/pq"
)

// grantAppRole creates or updates the API's restricted login and grants it
// what it needs on the migrated schema, as deploy/setup does for a server
// stack. It connects with the PG* variables, as psql would.
func grantAppRole(ctx context.Context) error {
	values, err := required("MISTY_APP_DB_USER", "MISTY_APP_DB_PASSWORD")
	if err != nil {
		return err
	}
	database, err := sql.Open("postgres", connection())
	if err != nil {
		return err
	}
	defer database.Close()
	role := pq.QuoteIdentifier(values["MISTY_APP_DB_USER"])
	password := pq.QuoteLiteral(values["MISTY_APP_DB_PASSWORD"])
	attributes := " LOGIN PASSWORD " + password + " NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"
	var exists bool
	if err := database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, values["MISTY_APP_DB_USER"]).Scan(&exists); err != nil {
		return fmt.Errorf("read database roles: %w", err)
	}
	var current string
	if err := database.QueryRowContext(ctx, `SELECT current_database()`).Scan(&current); err != nil {
		return fmt.Errorf("read database name: %w", err)
	}
	var statements []string
	if !exists {
		statements = append(statements, "CREATE ROLE "+role+attributes)
	}
	statements = append(statements,
		"ALTER ROLE "+role+" WITH"+attributes,
		"GRANT CONNECT ON DATABASE "+pq.QuoteIdentifier(current)+" TO "+role,
		"GRANT USAGE ON SCHEMA public TO "+role,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "+role,
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO "+role,
	)
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("grant the application role: %w", err)
		}
	}
	fmt.Println("Database application role synchronized and permissions granted.")
	return nil
}

// connection builds a URL from PGHOST, PGPORT, PGUSER, PGPASSWORD,
// PGDATABASE and PGSSLMODE; the development database has no TLS.
func connection() string {
	host := envOr("PGHOST", "localhost")
	address := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(os.Getenv("PGUSER"), os.Getenv("PGPASSWORD")),
		Host:   net.JoinHostPort(host, envOr("PGPORT", "5432")),
		Path:   "/" + os.Getenv("PGDATABASE"),
	}
	address.RawQuery = url.Values{"sslmode": {envOr("PGSSLMODE", "disable")}}.Encode()
	return address.String()
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
