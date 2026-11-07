// Package test holds the cluster tests of dbfwd. They run against a real
// Kubernetes cluster, which dbrun in dbmeta starts, and install each operator
// at a pinned release. docs/TESTING.md holds how to run them.
//
// This is a module of its own, so that its drivers and tools stay out of
// every build of dbfwd.
package test
