// Command dbfwd connects the native client of a database, or usql, to a
// database that is reachable only through a forward, such as a database that
// runs on Kubernetes.
//
//	dbfwd k+cnpg://pg.prod/
//	dbfwd usql k://pg-rw.prod/ -- -c 'select 1'
//	dbfwd forward k://my-pg.dev/
//	dbfwd lookup k://pg-rw.prod/
//	dbfwd list -A
//
// docs/CLI.md in the repository holds every command and flag.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/xo/dbmeta/container"
	"github.com/xo/dburl"
	"github.com/xo/ox"
	"github.com/xo/ox/otx"

	"github.com/xo/dbfwd"
	_ "github.com/xo/dbfwd/k8s"
)

func main() {
	ox.Run(
		ox.Usage("dbfwd", "connect a database client through a forward"),
		ox.Defaults(),
		ox.Exec(connect),
		ox.ValidArgs(1, -1),
		flags(true),
		ox.Sub(
			ox.Usage("usql", "connect usql through a forward"),
			ox.Exec(runUsql),
			ox.ValidArgs(1, -1),
			flags(false),
		),
		ox.Sub(
			ox.Usage("forward", "start a forward and hold it until Ctrl-C"),
			ox.Exec(forward),
			ox.ValidArgs(1, 1),
			flags(false),
		),
		ox.Sub(
			ox.Usage("lookup", "show what a name finds, with each step tried"),
			ox.Exec(lookup),
			ox.ValidArgs(1, 1),
			flags(false),
		),
		ox.Sub(
			ox.Usage("list", "list the databases that dbfwd can reach"),
			ox.Exec(list),
			ox.ValidArgs(0, 1),
			flags(false).
				Bool("all-namespaces", "list the databases of every namespace", ox.Short("A")).
				String("namespace", "list the databases of this namespace", ox.Short("n")).
				String("context", "use this context of the kubeconfig").
				Bool("wide", "show the owner, the roles, the ready pods, the databases and the credential", ox.Short("w")),
		),
		ox.Sub(
			ox.Usage("clients", "list the client of each database type"),
			ox.Exec(clients),
			ox.ValidArgs(0, 0),
		),
	)
}

// flags returns the flags of a command. The client flags belong to the
// command that runs a native client.
func flags(client bool) *ox.FlagSet {
	fs := ox.Flags().
		String("kubeconfig", "use the kubeconfig at this path in place of KUBECONFIG").
		String("kubectl", "run the kubectl at this path")
	if client {
		fs = fs.
			String("client", "run this client of the same database, such as pgcli").
			Bool("print", "print the client command, with the password masked, and run nothing")
	}
	return fs
}

// options builds the options of dbfwd from the flags.
func options(ctx context.Context) []dbfwd.Option {
	// dbfwd owns the terminal until the client starts, so a credential plugin
	// that prompts can use it
	opts := []dbfwd.Option{dbfwd.WithInteractive(func(run func(*os.File) error) error {
		return run(os.Stdin)
	})}
	if p := otx.String(ctx, "kubeconfig"); p != "" {
		opts = append(opts, dbfwd.WithEnv("KUBECONFIG", p))
	}
	if p := otx.String(ctx, "kubectl"); p != "" {
		opts = append(opts, dbfwd.WithTool("kubectl", p))
	}
	return opts
}

// parseURL reads the URL in the first argument.
func parseURL(arg string) (*dburl.URL, error) {
	u, err := dburl.Parse(arg)
	if err != nil {
		return nil, fmt.Errorf("reading the URL: %w", err)
	}
	if !dbfwd.Handles(u) {
		return nil, fmt.Errorf("reading the URL, whose scheme %q is not a forward: %w", u.SchemeName, dbfwd.ErrNoBackend)
	}
	return u, nil
}

// connect resolves the URL in args[0] and runs the native client of the
// database with args[1:].
func connect(ctx context.Context, args []string) error {
	u, err := parseURL(args[0])
	if err != nil {
		return err
	}
	fwd, err := dbfwd.Resolve(ctx, u, options(ctx)...)
	if err != nil {
		return err
	}
	defer fwd.Close()
	local, err := fwd.URL(ctx)
	if err != nil {
		return err
	}
	call, err := clientCall(fwd.Target.Type, otx.String(ctx, "client"), local)
	if err != nil {
		return err
	}
	call.Args = append(call.Args, args[1:]...)
	if otx.Bool(ctx, "print") {
		printCall(os.Stdout, call)
		return nil
	}
	return runChild(ctx, fwd, call.Command, call.Args, call.Env)
}

// clientCall builds the call of the native client for a database type, from
// the client table of dbmeta (D14). The password goes in the environment of
// the child. A client that takes it only as an argument is refused.
func clientCall(typ, override string, local *dburl.URL) (container.Call, error) {
	client, ok := container.ClientFor(typ)
	switch {
	case !ok:
		return container.Call{}, fmt.Errorf("finding the client of %s: %w; use dbfwd usql", typ, dbfwd.ErrUnsupported)
	case client.Command == "":
		return container.Call{}, fmt.Errorf("finding the client of %s, which has none because %s: %w; use dbfwd usql", typ, client.Why, dbfwd.ErrUnsupported)
	}
	conn, err := container.ParseConnection(local.String(), 0)
	if err != nil {
		return container.Call{}, fmt.Errorf("reading the local URL: %w", dbfwd.ErrBadURL)
	}
	call := client.Call(conn)
	if call.InArgs() {
		return container.Call{}, fmt.Errorf("running %s, which takes the password only as an argument: %w; use dbfwd usql or dbfwd forward", call.Command, dbfwd.ErrUnsupported)
	}
	if override != "" {
		call.Command = override
	}
	return call, nil
}

// printCall prints a call with the password masked, and the names of its
// environment variables.
func printCall(w io.Writer, call container.Call) {
	fmt.Fprintln(w, strings.Join(call.Masked(), " "))
	if names := call.EnvNames(); len(names) != 0 {
		fmt.Fprintln(w, "environment:", strings.Join(names, " "))
	}
}

// runUsql resolves the URL in args[0] and runs usql with the local URL in
// USQL_DSN, and args[1:] as its flags.
func runUsql(ctx context.Context, args []string) error {
	u, err := parseURL(args[0])
	if err != nil {
		return err
	}
	if err := checkUsqlArgs(args[1:]); err != nil {
		return err
	}
	fwd, err := dbfwd.Resolve(ctx, u, options(ctx)...)
	if err != nil {
		return err
	}
	defer fwd.Close()
	local, err := fwd.URL(ctx)
	if err != nil {
		return err
	}
	return runChild(ctx, fwd, "usql", args[1:], []string{"USQL_DSN=" + local.String()})
}

// checkUsqlArgs refuses a URL in the flags of usql, because a URL argument
// wins over USQL_DSN.
func checkUsqlArgs(args []string) error {
	for _, a := range args {
		if strings.Contains(a, "://") {
			return fmt.Errorf("passing a URL to usql, which wins over the forward: %w", dbfwd.ErrBadURL)
		}
	}
	return nil
}

// runChild runs a client on the terminal, with env added to the environment
// of the child only. Ctrl-C goes to the client, and dbfwd waits for it. When
// the client exits with a status, dbfwd closes the forward and exits with it.
func runChild(ctx context.Context, fwd *dbfwd.Forward, name string, args, env []string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // running the client that the person asked for is the purpose of the command, and no shell reads the arguments
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), env...)
	// catch Ctrl-C rather than ignore it, so that the client still gets it
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("running %s: %w", name, err)
	}
	go func() {
		select {
		case <-fwd.Done():
			if err := fwd.Err(); err != nil {
				fmt.Fprintln(os.Stderr, "dbfwd:", err)
			}
		case <-ctx.Done():
		}
	}()
	err := cmd.Wait()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		_ = fwd.Close()
		os.Exit(exitErr.ExitCode())
	}
	return err
}

// forward resolves the URL and holds the forward until Ctrl-C or until it is
// lost. It prints where to connect, and never the password.
func forward(ctx context.Context, args []string) error {
	u, err := parseURL(args[0])
	if err != nil {
		return err
	}
	fwd, err := dbfwd.Resolve(ctx, u, options(ctx)...)
	if err != nil {
		return err
	}
	defer fwd.Close()
	local, err := fwd.URL(ctx)
	if err != nil {
		return err
	}
	fmt.Println("forwarding to", fwd)
	fmt.Println("address:", local.Host)
	fmt.Println("user:", fwd.User)
	if fwd.Database != "" {
		fmt.Println("database:", fwd.Database)
	}
	fmt.Println("press Ctrl-C to stop")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	select {
	case <-sig:
		return nil
	case <-fwd.Done():
		return fwd.Err()
	case <-ctx.Done():
		return nil
	}
}

// lookup runs the lookup and prints each step and what matched.
func lookup(ctx context.Context, args []string) error {
	u, err := parseURL(args[0])
	if err != nil {
		return err
	}
	m, err := dbfwd.Lookup(ctx, u, options(ctx)...)
	var le *dbfwd.LookupError
	switch {
	case errors.As(err, &le):
		printSteps(os.Stdout, le.Steps)
		return err
	case err != nil:
		return err
	}
	printSteps(os.Stdout, m.Steps)
	fmt.Println("match:", m.Target)
	fmt.Println("type:", m.Target.Type)
	return nil
}

// printSteps prints the steps of a lookup.
func printSteps(w io.Writer, steps []dbfwd.Step) {
	for _, s := range steps {
		fmt.Fprintf(w, "%-34s %s\n", s.Kind, s.Result)
	}
}

// types are the database types that the k8s backend can resolve. A type that
// is missing here still resolves, and only this list skips it.
var types = []string{"postgres", "mysql", "mariadb", "sqlserver", "clickhouse"}

// clients prints the client of each database type, and whether it is on the
// PATH.
func clients(context.Context) error {
	for _, typ := range types {
		client, ok := container.ClientFor(typ)
		switch {
		case !ok:
			fmt.Printf("%-12s none\n", typ)
		case client.Command == "":
			fmt.Printf("%-12s none: %s\n", typ, client.Why)
		default:
			where := "not on the PATH"
			if p, err := exec.LookPath(client.Command); err == nil {
				where = p
			}
			fmt.Printf("%-12s %-20s %s\n", typ, client.Command, where)
		}
	}
	where := "not on the PATH"
	if p, err := exec.LookPath("usql"); err == nil {
		where = p
	}
	fmt.Printf("%-12s %-20s %s\n", "any", "usql", where)
	return nil
}
