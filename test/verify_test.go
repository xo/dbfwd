package test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/gocql/gocql"
	"github.com/xo/dburl"

	"github.com/xo/dbfwd"
)

// verify connects through a forward with the protocol of its database, and
// returns the user and the database that the server reports. A protocol that
// reports neither returns the user of the forward once a request with its
// credentials works.
func verify(ctx context.Context, t *testing.T, fwd *dbfwd.Forward) (string, string) {
	t.Helper()
	local, err := fwd.URL(ctx)
	if err != nil {
		t.Fatalf("reading the local URL: %v", err)
	}
	var user, database string
	retry(t, func() error {
		var err error
		user, database, err = probe(ctx, local, fwd)
		return err
	})
	return user, database
}

// retry calls fn until it works or a minute passes, because a server behind
// a forward can take a moment to accept connections.
func retry(t *testing.T, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		err := fn()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("connecting through the forward: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}

// probe makes one connection and one request.
func probe(ctx context.Context, local *dburl.URL, fwd *dbfwd.Forward) (string, string, error) {
	pass, _ := local.User.Password()
	switch local.SchemeName {
	case "postgres":
		return sqlPair(ctx, local, "SELECT current_user, current_database()")
	case "cratedb":
		return sqlPair(ctx, local, "SELECT CURRENT_USER, ''")
	case "mysql", "tidb":
		return sqlPair(ctx, local, "SELECT SUBSTRING_INDEX(CURRENT_USER(), '@', 1), COALESCE(DATABASE(), '')")
	case "vitess":
		// vtgate reports its own user for CURRENT_USER, so only the login and
		// the keyspace count
		_, db, err := sqlPair(ctx, local, "SELECT 'x', COALESCE(DATABASE(), '')")
		return fwd.User, db, err
	case "clickhouse":
		return sqlPair(ctx, local, "SELECT currentUser(), currentDatabase()")
	case "cassandra":
		return fwd.User, "", cql(local, fwd.User, pass)
	case "elasticsearch", "opensearch":
		return fwd.User, "", get(ctx, local, "/", fwd.User, pass)
	case "solr":
		return fwd.User, "", get(ctx, local, "/solr/admin/info/system?wt=json", fwd.User, pass)
	case "arangodb":
		return fwd.User, "", get(ctx, local, "/_api/version", fwd.User, pass)
	case "couchbase":
		return fwd.User, "", get(ctx, local, "/query/service?statement=SELECT%201", fwd.User, pass)
	}
	return "", "", fmt.Errorf("no probe for the scheme %s", local.SchemeName)
}

// sqlPair opens a database with database/sql and scans two strings.
func sqlPair(ctx context.Context, local *dburl.URL, stmt string) (string, string, error) {
	u := local
	if local.SchemeName == "tidb" || local.SchemeName == "vitess" {
		// both speak the MySQL protocol, through the MySQL driver
		mu, err := dburl.Parse(strings.Replace(local.String(), local.SchemeName+"://", "mysql://", 1))
		if err != nil {
			return "", "", err
		}
		u = mu
	}
	db, err := sql.Open(u.Driver, u.DSN)
	if err != nil {
		return "", "", err
	}
	defer db.Close()
	var a, b string
	err = db.QueryRowContext(ctx, stmt).Scan(&a, &b)
	return a, b, err
}

// cql connects with the Cassandra protocol and reads the release.
func cql(local *dburl.URL, user, pass string) error {
	cluster := gocql.NewCluster(local.Hostname())
	cluster.Port, _ = strconv.Atoi(local.Port())
	cluster.DisableInitialHostLookup = true
	cluster.Timeout = 10 * time.Second
	cluster.ConnectTimeout = 10 * time.Second
	if user != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{Username: user, Password: pass}
	}
	s, err := cluster.CreateSession()
	if err != nil {
		return err
	}
	defer s.Close()
	var release string
	return s.Query("SELECT release_version FROM system.local").Scan(&release)
}

// get sends an HTTP GET to the forward with basic auth, and wants a 2xx.
func get(ctx context.Context, local *dburl.URL, path, user, pass string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+local.Host+path, nil)
	if err != nil {
		return err
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 300))
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s: %s: %s", path, res.Status, body)
	}
	return nil
}
