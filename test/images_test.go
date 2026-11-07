package test

import (
	"testing"
)

// imagesFixture runs the official images with no operator, in the ways that
// charts and hand-written manifests pass a password: a secretKeyRef, a _FILE
// variable over a Secret volume, and envFrom. One Service carries the label
// of a Helm release, and one pod has an exporter beside the database.
const imagesFixture = `
apiVersion: v1
kind: Secret
metadata: {name: db-creds}
stringData: {password: shop-test-only, root: root-test-only, POSTGRES_PASSWORD: env-test-only}
---
apiVersion: v1
kind: Service
metadata:
  name: pgsts
  labels: {app.kubernetes.io/instance: pgrel, app.kubernetes.io/name: postgresql}
spec:
  selector: {app: pgsts}
  ports: [{port: 5432, targetPort: pg}]
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: pgsts}
spec:
  serviceName: pgsts
  replicas: 1
  selector: {matchLabels: {app: pgsts}}
  template:
    metadata: {labels: {app: pgsts}}
    spec:
      containers:
      - name: postgres
        image: postgres:18
        ports: [{name: pg, containerPort: 5432}]
        env:
        - {name: POSTGRES_USER, value: shop}
        - {name: POSTGRES_DB, value: shop}
        - name: POSTGRES_PASSWORD
          valueFrom: {secretKeyRef: {name: db-creds, key: password}}
        readinessProbe:
          exec: {command: [pg_isready, -U, shop, -h, 127.0.0.1]}
          periodSeconds: 2
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: mysqldep}
spec:
  replicas: 1
  selector: {matchLabels: {app: mysqldep}}
  template:
    metadata: {labels: {app: mysqldep}}
    spec:
      containers:
      - name: exporter
        image: prom/mysqld-exporter:v0.17.2
        args: [--mysqld.address=127.0.0.1:3306, --mysqld.username=root]
        env:
        - name: MYSQLD_EXPORTER_PASSWORD
          valueFrom: {secretKeyRef: {name: db-creds, key: root}}
      - name: mysql
        image: mysql:8.4
        ports: [{containerPort: 3306}]
        env:
        - {name: MYSQL_USER, value: shop}
        - {name: MYSQL_DATABASE, value: shop}
        - name: MYSQL_PASSWORD
          valueFrom: {secretKeyRef: {name: db-creds, key: password}}
        - name: MYSQL_ROOT_PASSWORD
          valueFrom: {secretKeyRef: {name: db-creds, key: root}}
        readinessProbe:
          exec: {command: [sh, -c, "mysqladmin ping -h 127.0.0.1 -uroot -p$MYSQL_ROOT_PASSWORD"]}
          periodSeconds: 2
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: mariadbfile}
spec:
  serviceName: mariadbfile
  replicas: 1
  selector: {matchLabels: {app: mariadbfile}}
  template:
    metadata: {labels: {app: mariadbfile}}
    spec:
      containers:
      - name: mariadb
        image: mariadb:11.8
        ports: [{containerPort: 3306}]
        env:
        - {name: MARIADB_ROOT_PASSWORD_FILE, value: /run/secrets/db/root}
        - {name: MARIADB_DATABASE, value: shop}
        volumeMounts: [{name: creds, mountPath: /run/secrets/db, readOnly: true}]
        readinessProbe:
          exec: {command: [healthcheck.sh, --connect, --innodb_initialized]}
          periodSeconds: 2
      volumes:
      - {name: creds, secret: {secretName: db-creds}}
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: envfrom}
spec:
  serviceName: envfrom
  replicas: 1
  selector: {matchLabels: {app: envfrom}}
  template:
    metadata: {labels: {app: envfrom}}
    spec:
      containers:
      - name: postgres
        image: postgres:18
        ports: [{containerPort: 5432}]
        env: [{name: POSTGRES_USER, value: app}]
        envFrom: [{secretRef: {name: db-creds}}]
        readinessProbe:
          exec: {command: [pg_isready, -U, app, -h, 127.0.0.1]}
          periodSeconds: 2
`

func TestOfficialImages(t *testing.T) {
	suite{
		name:    "images",
		fixture: imagesFixture,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			for _, w := range []string{"statefulset/pgsts", "deployment/mysqldep", "statefulset/mariadbfile", "statefulset/envfrom"} {
				rollout(t, ns, w)
			}
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k://pgsts." + ns + "/", user: "shop", database: "shop", kind: "Service"},
				{url: "k://pgrel." + ns + "/", user: "shop", database: "shop", kind: "Service"},
				{url: "k://pgsts-0." + ns + "/", user: "shop", database: "shop", kind: "Pod"},
				{url: "k+postgres://pgsts-0." + ns + "/", user: "shop", database: "shop", kind: "Pod"},
				{url: "k://mysqldep." + ns + "/", user: "shop", database: "shop", kind: "Deployment"},
				{url: "k://root@mysqldep." + ns + "/", user: "root", database: "shop", kind: "Deployment"},
				{url: "k://mariadbfile." + ns + "/", user: "root", database: "shop", kind: "StatefulSet"},
				{url: "k://envfrom-0." + ns + "/", user: "app", database: "app", kind: "Pod"},
				{url: "k://db-creds:password@pgsts." + ns + "/", user: "shop", database: "shop", kind: "Service"},
			}
		},
	}.run(t)
}

// TestReconnect deletes the pod behind a forward, and checks that the same
// URL reaches the pod that replaces it (D16).
func TestReconnect(t *testing.T) {
	skip(t)
	ns := c.namespace(t, "reconnect")
	if c.existing == "" {
		t.Cleanup(func() { _, _ = c.run("delete", "namespace", ns, "--wait=false") })
	}
	c.apply(t, ns, imagesFixture)
	rollout(t, ns, "statefulset/pgsts")
	for _, raw := range []string{"k://pgsts-0." + ns + "/", "k://pgsts." + ns + "/"} {
		t.Run(caseName(raw), func(t *testing.T) {
			fwd := c.connect(t, "", check{url: raw, user: "shop", database: "shop"})
			before, err := fwd.URL(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			c.kubectl(t, "--namespace", ns, "delete", "pod", "pgsts-0", "--wait=true")
			rollout(t, ns, "statefulset/pgsts")
			user, database := query(t.Context(), t, fwd)
			if user != "shop" || database != "shop" {
				t.Errorf("after the pod was replaced, connected as %s to %s, expected shop to shop", user, database)
			}
			after, err := fwd.URL(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if after.Host != before.Host {
				t.Errorf("local address = %s after the reconnect, expected %s", after.Host, before.Host)
			}
			select {
			case <-fwd.Done():
				t.Errorf("the forward stopped: %v", fwd.Err())
			default:
			}
		})
	}
}

func TestBitnamiPostgres(t *testing.T) {
	suite{
		name: "bitnami",
		ready: func(t *testing.T, ns string) {
			t.Helper()
			c.helm(t, "upgrade", "--install", "my-pg", "oci://registry-1.docker.io/bitnamicharts/postgresql", "--namespace", ns,
				"--set", "global.security.allowInsecureImages=true",
				"--set", "image.repository=bitnamilegacy/postgresql",
				"--set", "primary.persistence.size=1Gi", "--wait", "--timeout", "10m")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k://my-pg." + ns + "/", user: "postgres", database: "postgres", kind: "Service"},
				{url: "k://my-pg-postgresql." + ns + "/", user: "postgres", database: "postgres", kind: "Service"},
				{url: "k://my-pg-postgresql-0." + ns + "/", user: "postgres", database: "postgres", kind: "Pod"},
			}
		},
	}.run(t)
}
