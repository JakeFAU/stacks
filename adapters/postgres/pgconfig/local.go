// Package pgconfig owns bounded PostgreSQL connection-string validation.
package pgconfig

import (
	"errors"
	"net"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var postgresConnectionEnvironment = []string{
	"PGHOST",
	"PGHOSTADDR",
	"PGPORT",
	"PGDATABASE",
	"PGUSER",
	"PGPASSWORD",
	"PGPASSFILE",
	"PGAPPNAME",
	"PGCONNECT_TIMEOUT",
	"PGSSLMODE",
	"PGSSLKEY",
	"PGSSLCERT",
	"PGSSLSNI",
	"PGSSLROOTCERT",
	"PGSSLPASSWORD",
	"PGSSLNEGOTIATION",
	"PGTARGETSESSIONATTRS",
	"PGSERVICE",
	"PGSERVICEFILE",
	"PGTZ",
	"PGOPTIONS",
	"PGMINPROTOCOLVERSION",
	"PGMAXPROTOCOLVERSION",
	"PGCHANNELBINDING",
	"PGREQUIREAUTH",
}

var allowedConnectionKeys = []string{
	"host",
	"port",
	"database",
	"dbname",
	"user",
	"password",
	"passfile",
	"connect_timeout",
	"sslmode",
	"sslkey",
	"sslcert",
	"sslrootcert",
	"sslnegotiation",
	"sslpassword",
	"sslsni",
	"krbspn",
	"krbsrvname",
	"target_session_attrs",
	"min_protocol_version",
	"max_protocol_version",
	"channel_binding",
	"require_auth",
	"application_name",
	"search_path",
	"default_query_exec_mode",
	"statement_cache_capacity",
	"description_cache_capacity",
}

// ParseLocal applies pgx v5 connection parsing while rejecting redirection
// parameters and every non-loopback primary or fallback endpoint.
func ParseLocal(connectionString string) (*pgx.ConnConfig, error) {
	for _, variable := range postgresConnectionEnvironment {
		if value, present := os.LookupEnv(variable); present && value != "" {
			return nil, errors.New("ambient PostgreSQL connection configuration is not allowed")
		}
	}
	if strings.TrimSpace(connectionString) == "" {
		return nil, errors.New("local PostgreSQL connection configuration is required")
	}
	config, err := pgx.ParseConfigWithOptions(
		connectionString,
		pgx.ParseConfigOptions{ParseConfigOptions: pgconn.ParseConfigOptions{
			ConnStringAllowedKeys: allowedConnectionKeys,
		}},
	)
	if err != nil {
		return nil, errors.New("local PostgreSQL connection configuration is invalid")
	}
	if strings.TrimSpace(config.Database) == "" ||
		strings.TrimSpace(config.User) == "" ||
		!isLoopbackHost(config.Host) {
		return nil, errors.New("local PostgreSQL connection configuration is invalid")
	}
	for _, fallback := range config.Fallbacks {
		if fallback == nil || !isLoopbackHost(fallback.Host) {
			return nil, errors.New("local PostgreSQL connection configuration is invalid")
		}
	}
	return config, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address := net.ParseIP(strings.TrimSpace(host))
	return address != nil && address.IsLoopback()
}
