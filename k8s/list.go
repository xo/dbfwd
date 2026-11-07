package k8s

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/xo/dburl"

	"github.com/xo/dbfwd"
)

// allNamespaces is the namespace of a listing of every namespace.
const allNamespaces = "*"

// listTimeout is how long one kubectl call of a listing waits for the
// cluster.
const listTimeout = "30s"

// workloadKinds are the kinds that every listing reads, besides the kinds of
// the operators. A ReplicaSet links a pod to its Deployment.
var workloadKinds = []string{"services", "statefulsets.apps", "deployments.apps", "replicasets.apps", "pods"}

// scope is what a listing URL asks for (D18).
type scope struct {
	// pattern is the name, which can hold "*".
	pattern string

	// namespace is empty for the namespace of the context, "*" for every
	// namespace, or a namespace.
	namespace string

	// typ is the operator or the database type after the +.
	typ string

	// context comes from the query.
	context string
}

// parseScope reads a listing URL. Its errors never hold the URL.
func parseScope(u *dburl.URL) (scope, error) {
	var s scope
	host := u.Hostname()
	if host == "" {
		return s, fmt.Errorf("reading the host: %w", dbfwd.ErrBadURL)
	}
	if name, namespace, ok := strings.CutLast(host, "."); ok {
		s.pattern, s.namespace = name, namespace
	} else {
		s.pattern = host
	}
	if s.pattern == "" || (strings.Contains(host, ".") && s.namespace == "") {
		return s, fmt.Errorf("reading the host %q: %w", host, dbfwd.ErrBadURL)
	}
	if strings.Contains(s.namespace, "*") && s.namespace != allNamespaces {
		return s, fmt.Errorf("reading the namespace %q, which is a name or *: %w", s.namespace, dbfwd.ErrBadURL)
	}
	if _, err := path.Match(s.pattern, ""); err != nil {
		return s, fmt.Errorf("reading the name %q: %w", s.pattern, dbfwd.ErrBadURL)
	}
	switch {
	case u.User != nil:
		return s, fmt.Errorf("reading a listing, which takes no user part: %w", dbfwd.ErrBadURL)
	case u.Port() != "":
		return s, fmt.Errorf("reading a listing, which takes no port: %w", dbfwd.ErrBadURL)
	case u.Path != "" && u.Path != "/":
		return s, fmt.Errorf("reading a listing, which takes no database: %w", dbfwd.ErrBadURL)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return s, fmt.Errorf("reading the query: %w", dbfwd.ErrBadURL)
	}
	if q.Has(keyRole) {
		return s, fmt.Errorf("reading a listing, which takes no role: %w", dbfwd.ErrBadURL)
	}
	s.context = q.Get(keyContext)
	s.typ = typeKey(strings.ToLower(u.Transport))
	return s, nil
}

// List lists the databases that u names (D18). It reads no Secret and starts
// no forward.
func (b backend) List(ctx context.Context, u *dburl.URL, cfg dbfwd.Config) (*dbfwd.Listing, error) {
	sc, err := parseScope(u)
	if err != nil {
		return nil, err
	}
	r := b.newRunner(cfg)
	context, namespace, err := current(ctx, r, sc.context)
	if err != nil {
		return nil, err
	}
	all := sc.namespace == allNamespaces
	switch {
	case all:
		namespace = allNamespaces
	case sc.namespace != "":
		namespace = sc.namespace
	}
	k := &kube{r: r, context: sc.context, namespace: namespace}
	if all {
		k.namespace = ""
	}
	listing := &dbfwd.Listing{Scope: []dbfwd.Field{{Name: "context", Value: context}, {Name: "namespace", Value: namespace}}}
	// the first call that reaches the cluster gets the terminal, as in a
	// lookup
	installed, err := k.resources(ctx)
	switch {
	case errors.Is(err, dbfwd.ErrUnauthorized), errors.Is(err, dbfwd.ErrNoTool), ctx.Err() != nil:
		return nil, err
	case err != nil:
		installed = nil
		listing.Skipped = append(listing.Skipped, dbfwd.Skip{Scope: listing.Scope, Kind: "custom resources", Reason: "the cluster did not say which kinds it has"})
	}
	items, skipped, err := k.readAll(ctx, listKinds(installed), all, listing.Scope)
	if err != nil {
		return nil, err
	}
	listing.Skipped = append(listing.Skipped, skipped...)
	inv := newInventory(items)
	for _, e := range inv.entries(context, sc.context) {
		if sc.matches(e) {
			listing.Entries = append(listing.Entries, e)
		}
	}
	slices.SortFunc(listing.Entries, func(a, b dbfwd.Entry) int {
		return cmp.Or(
			cmp.Compare(a.Target.Scope[1].Value, b.Target.Scope[1].Value),
			cmp.Compare(a.Target.Name, b.Target.Name),
			cmp.Compare(a.Target.Kind, b.Target.Kind),
		)
	})
	return listing, nil
}

// matches reports whether an entry fits the name and the type of the scope.
func (s scope) matches(e dbfwd.Entry) bool {
	if ok, _ := path.Match(s.pattern, e.Target.Name); !ok {
		return false
	}
	switch op := operatorFor(s.typ); {
	case op != nil:
		return e.Operator == op.name
	case s.typ != "":
		return e.Target.Type == s.typ
	}
	return true
}

// listKinds returns the kinds that a listing reads: every installed kind of
// every operator, and the workloads. With installed nil, the cluster did not
// say which kinds it has, and a kind that is not installed fails the whole
// call, so only the workloads are read.
func listKinds(installed map[string]bool) []string {
	var kinds []string
	if installed != nil {
		for _, op := range operators {
			if installed[op.resource] {
				kinds = append(kinds, op.resource)
			}
			for _, c := range op.children {
				if installed[c.resource] {
					kinds = append(kinds, c.resource)
				}
			}
		}
	}
	return append(kinds, workloadKinds...)
}

// readAll reads every object of the kinds in one call. When RBAC forbids a
// kind, it reads each kind alone, lists the forbidden ones as skipped, and
// fails only when every kind is forbidden.
func (k *kube) readAll(ctx context.Context, kinds []string, all bool, where []dbfwd.Field) ([]object, []dbfwd.Skip, error) {
	items, err := k.getAll(ctx, kinds, all)
	switch {
	case err == nil:
		return items, nil, nil
	case !errors.Is(err, dbfwd.ErrForbidden):
		return nil, nil, fmt.Errorf("listing the databases: %w", err)
	}
	items = nil
	var skipped []dbfwd.Skip
	for _, kind := range kinds {
		got, err := k.getAll(ctx, []string{kind}, all)
		switch {
		case err == nil:
			items = append(items, got...)
		case errors.Is(err, dbfwd.ErrForbidden):
			skipped = append(skipped, dbfwd.Skip{Scope: where, Kind: kind, Reason: resultNotAllowed})
		default:
			return nil, nil, fmt.Errorf("listing %s: %w", kind, err)
		}
	}
	if len(skipped) == len(kinds) {
		if all {
			return nil, nil, fmt.Errorf("listing every namespace: %w", dbfwd.ErrForbidden)
		}
		return nil, nil, fmt.Errorf("listing the namespace %s: %w", k.namespace, dbfwd.ErrForbidden)
	}
	return items, skipped, nil
}

// getAll reads every object of the kinds, in the namespace or in every
// namespace.
func (k *kube) getAll(ctx context.Context, kinds []string, all bool) ([]object, error) {
	args := []string{"get", strings.Join(kinds, ","), "--output", "json", "--request-timeout", listTimeout}
	if all {
		args = append(args, "--all-namespaces")
	}
	out, err := k.r.get(ctx, k.args(args...)...)
	if err != nil {
		return nil, err
	}
	var o object
	if err := json.Unmarshal(out, &o); err != nil {
		return nil, fmt.Errorf("reading the listing: %w", err)
	}
	return o.list("items"), nil
}

// inventory holds the objects of a listing, by namespace, group, kind and
// name.
type inventory struct {
	objects []object
	byKey   map[string]object
}

// newInventory indexes the objects of a listing.
func newInventory(items []object) *inventory {
	inv := &inventory{objects: items, byKey: make(map[string]object, len(items))}
	for _, o := range items {
		inv.byKey[keyOf(o)] = o
	}
	return inv
}

// objectKey is the key of an object in an inventory.
func objectKey(namespace, group, kind, name string) string {
	return namespace + "/" + group + "/" + kind + "/" + name
}

// keyOf returns the key of an object.
func keyOf(o object) string {
	return objectKey(namespaceOf(o), groupOf(o), o.str("kind"), o.name())
}

// namespaceOf returns the namespace of an object.
func namespaceOf(o object) string {
	return o.str("metadata", "namespace")
}

// groupOf returns the API group of an object, which is empty for the core
// kinds.
func groupOf(o object) string {
	group, _, ok := strings.Cut(o.str("apiVersion"), "/")
	if !ok {
		return ""
	}
	return group
}

// find returns the object with the group, kind and name in a namespace.
func (inv *inventory) find(namespace, group, kind, name string) (object, bool) {
	o, ok := inv.byKey[objectKey(namespace, group, kind, name)]
	return o, ok
}

// findCR returns the cluster CR of an operator with the name.
func (inv *inventory) findCR(op *operator, namespace, name string) (object, bool) {
	for _, g := range op.groups {
		if o, ok := inv.find(namespace, g, op.kind, name); ok {
			return o, true
		}
	}
	return nil, false
}

// ofKind returns the objects of a kind in any of the groups.
func (inv *inventory) ofKind(groups []string, kind string) []object {
	var out []object
	for _, o := range inv.objects {
		if o.str("kind") == kind && slices.Contains(groups, groupOf(o)) {
			out = append(out, o)
		}
	}
	return out
}

// root follows the controller of an object, within the inventory, to the
// object at the top of its chain.
func (inv *inventory) root(o object) object {
	for range 6 {
		ref, ok := o.controller()
		if !ok {
			return o
		}
		next, ok := inv.find(namespaceOf(o), ref.group, ref.kind, ref.name)
		if !ok {
			return o
		}
		o = next
	}
	return o
}

// podsOf returns the pods in a namespace whose chain ends at top, or that a
// selector selects.
func (inv *inventory) podsOf(namespace string, top object, selectors ...string) []object {
	var sels []selector
	for _, s := range selectors {
		if s == "" {
			continue
		}
		if sel, err := parseSelector(s); err == nil {
			sels = append(sels, sel)
		}
	}
	topKey := ""
	if top != nil {
		topKey = keyOf(top)
	}
	var out []object
	for _, p := range inv.ofKind([]string{""}, "Pod") {
		if namespaceOf(p) != namespace {
			continue
		}
		hit := topKey != "" && keyOf(inv.root(p)) == topKey
		for _, sel := range sels {
			hit = hit || sel.matches(p.labels())
		}
		if hit {
			out = append(out, p)
		}
	}
	return out
}

// reaches reports whether what a rule found has a Service or a pod in the
// inventory.
func (inv *inventory) reaches(namespace string, f *found) bool {
	if svc, ok := strings.CutPrefix(f.forward, "service/"); ok {
		_, ok := inv.find(namespace, "", "Service", svc)
		return ok
	}
	return len(inv.podsOf(namespace, nil, f.pods)) > 0
}

// row is an entry and the object it stands for.
type row struct {
	entry dbfwd.Entry
	obj   object
	op    *operator
}

// entries builds one entry for each database in the inventory. context is
// the resolved context, and named is the context that the URL named, or
// empty.
func (inv *inventory) entries(context, named string) []dbfwd.Entry {
	var rows []row
	rows = append(rows, inv.operatorRows(context)...)
	rows = append(rows, inv.workloadRows(context)...)
	out := make([]dbfwd.Entry, 0, len(rows))
	for _, r := range rows {
		r.entry.URL = inv.shortestURL(r, named)
		out = append(out, r.entry)
	}
	return out
}

// operatorRows builds a row for each cluster CR, and for each child that
// names another database or another way in.
func (inv *inventory) operatorRows(context string) []row {
	var rows []row
	clusters := map[string]dbfwd.Entry{}
	for _, op := range operators {
		for _, cr := range inv.ofKind(op.groups, op.kind) {
			if inv.foldsInto(op, cr) {
				continue
			}
			f, err := op.build(cr, "")
			if err != nil {
				// a cluster that dbfwd cannot reach, such as MongoDB
				continue
			}
			ns := namespaceOf(cr)
			e := newEntry(context, ns, op, f)
			e.Target.Kind, e.Target.Name = op.kind, cr.name()
			e.Target.Owner = op.name + " " + op.kind + " " + cr.name()
			e.Roles = []string{roleRW}
			selectors := []string{f.pods}
			if ro, err := op.build(cr, roleRO); err == nil && ro.forward != f.forward && inv.reaches(ns, ro) {
				e.Roles = append(e.Roles, roleRO)
				selectors = append(selectors, ro.pods)
			}
			countPods(&e, inv.podsOf(ns, cr, selectors...))
			clusters[keyOf(cr)] = e
			rows = append(rows, row{entry: e, obj: cr, op: op})
		}
	}
	for _, op := range operators {
		for _, c := range op.children {
			for _, obj := range inv.ofKind(op.groups, c.kind) {
				ns := namespaceOf(obj)
				cr, ok := inv.findCR(op, ns, c.cluster(obj))
				if !ok {
					continue
				}
				f, err := op.build(cr, c.role(obj))
				if err != nil {
					continue
				}
				c.apply(obj, f)
				e := newEntry(context, ns, op, f)
				e.Target.Kind, e.Target.Name = c.kind, obj.name()
				e.Target.Owner = op.name + " " + op.kind + " " + cr.name()
				e.Roles = []string{pick(f.role)}
				if pods := inv.podsOf(ns, obj); len(pods) > 0 {
					countPods(&e, pods)
				} else if parent, ok := clusters[keyOf(cr)]; ok {
					// a child with no pods of its own, such as a Database,
					// runs on the pods of its cluster
					e.Ready, e.Pods = parent.Ready, parent.Pods
				}
				rows = append(rows, row{entry: e, obj: obj, op: op})
			}
		}
	}
	return rows
}

// foldsInto reports whether a cluster CR belongs to the row of another CR:
// one that is its controller, or the OpenEverest DatabaseCluster of the same
// name, which makes a Percona cluster with no owner reference.
func (inv *inventory) foldsInto(op *operator, cr object) bool {
	if ref, ok := cr.controller(); ok {
		for _, other := range operators {
			if other != op && other.owns(ref.kind, ref.group) {
				return true
			}
		}
	}
	if op == perconaXtraDB || op == perconaServer {
		if _, ok := inv.findCR(everest, namespaceOf(cr), cr.name()); ok {
			return true
		}
	}
	return false
}

// workloadRows builds a row for each StatefulSet, Deployment or pod that
// nothing controls, that no operator claims by its labels, and whose pod spec
// shows a sign of a database: an image or an environment that a pod spec
// rule knows.
func (inv *inventory) workloadRows(context string) []row {
	type workload struct {
		group, kind string
		spec        []string
		labels      []string
	}
	var rows []row
	for _, w := range []workload{
		{"apps", "StatefulSet", []string{"spec", "template", "spec"}, []string{"spec", "template", "metadata", "labels"}},
		{"apps", "Deployment", []string{"spec", "template", "spec"}, []string{"spec", "template", "metadata", "labels"}},
		{"", "Pod", []string{"spec"}, []string{"metadata", "labels"}},
	} {
		for _, obj := range inv.ofKind([]string{w.group}, w.kind) {
			if _, ok := obj.controller(); ok || claimed(obj.labels()) {
				continue
			}
			spec := object(asMap(obj.get(w.spec...)))
			if !hasSign(spec) {
				continue
			}
			labels := object(asMap(obj.get(w.labels...)))
			f, err := fromPodSpec(spec, "", labels.str("app.kubernetes.io/name"), "")
			if err != nil {
				continue
			}
			ns := namespaceOf(obj)
			e := newEntry(context, ns, nil, f)
			e.Target.Kind, e.Target.Name = w.kind, obj.name()
			if l := obj.labels(); l["app.kubernetes.io/managed-by"] == "Helm" && l["app.kubernetes.io/instance"] != "" {
				e.Target.Owner = "Helm release " + l["app.kubernetes.io/instance"]
			}
			e.Roles = []string{roleRW}
			countPods(&e, inv.podsOf(ns, obj))
			rows = append(rows, row{entry: e, obj: obj})
		}
	}
	return rows
}

// claimed reports whether an operator that sets no owner reference claims an
// object by its labels.
func claimed(labels map[string]string) bool {
	for _, op := range operators {
		if op.fromLabels != nil && op.fromLabels(labels) != "" {
			return true
		}
	}
	return false
}

// hasSign reports whether a pod spec shows a sign of a database: a container
// image or an environment that a pod spec rule knows.
func hasSign(spec object) bool {
	c, ok := pickContainer(spec, "")
	if !ok {
		return false
	}
	if imageType(c.str("image")) != nil {
		return true
	}
	t, err := envType(readEnv(spec, c))
	return err == nil && t != nil
}

// newEntry starts an entry for what a rule found.
func newEntry(context, namespace string, op *operator, f *found) dbfwd.Entry {
	e := dbfwd.Entry{
		Target: dbfwd.Target{
			Backend: name,
			Scope:   []dbfwd.Field{{Name: "context", Value: context}, {Name: "namespace", Value: namespace}},
			Type:    cmp.Or(f.target.Type, f.scheme),
		},
		Credential: source(f.account.password),
	}
	if op != nil {
		e.Operator = op.name
	}
	if db := f.database; db.set && db.secret == "" && db.value != "" {
		e.Databases = []string{db.value}
	}
	return e
}

// countPods sets the pods of an entry, and how many are ready.
func countPods(e *dbfwd.Entry, pods []object) {
	e.Pods = len(pods)
	for _, p := range pods {
		if ready(p) {
			e.Ready++
		}
	}
}

// source says where a credential comes from, and never holds its value.
func source(c credential) string {
	switch {
	case !c.set:
		return ""
	case c.secret != "":
		return "Secret " + c.secret + " key " + c.key
	}
	return "pod spec"
}

// shortestURL returns the shortest URL that Resolve maps to the database of a
// row: k://<name>.<namespace>/ when the name reaches it, and with the key of
// the operator when another object of the name comes first in the lookup.
// named is the context that the listing named, which the URL keeps.
func (inv *inventory) shortestURL(r row, named string) string {
	ns, nm := namespaceOf(r.obj), r.obj.name()
	scheme := "k"
	if first, ok := inv.firstMatch(ns, nm, operators); ok && !inv.sameDatabase(first, r.obj) && r.op != nil {
		scheme = "k+" + r.op.key
	}
	u := scheme + "://" + nm + "." + ns + "/"
	if named != "" {
		u += "?" + url.Values{keyContext: {named}}.Encode()
	}
	return u
}

// firstMatch returns the object that the lookup of docs/LOOKUP.md matches
// first for a name, among the objects of the inventory. It leaves out the
// Helm release, which a listing does not read as such.
func (inv *inventory) firstMatch(namespace, nm string, ops []*operator) (object, bool) {
	for _, op := range ops {
		if o, ok := inv.findCR(op, namespace, nm); ok {
			return o, true
		}
	}
	for _, op := range ops {
		for _, c := range op.children {
			for _, g := range op.groups {
				if o, ok := inv.find(namespace, g, c.kind, nm); ok {
					return o, true
				}
			}
		}
	}
	for _, k := range [][2]string{{"", "Service"}, {"apps", "StatefulSet"}, {"apps", "Deployment"}, {"", "Pod"}} {
		if o, ok := inv.find(namespace, k[0], k[1], nm); ok {
			return o, true
		}
	}
	return nil, false
}

// sameDatabase reports whether an object leads to the database of the row of
// top: it is top, its chain ends at top, or it is a Service that selects a pod
// of top.
func (inv *inventory) sameDatabase(o, top object) bool {
	topKey := keyOf(top)
	if keyOf(inv.root(o)) == topKey {
		return true
	}
	if o.str("kind") != "Service" {
		return false
	}
	sel := selectorString(o.strmap("spec", "selector"))
	if sel == "" {
		return false
	}
	return inv.selectsOwnPod(o, top, sel)
}

// selectsOwnPod reports whether a selector picks a pod whose chain ends at
// top.
func (inv *inventory) selectsOwnPod(svc, top object, sel string) bool {
	s, err := parseSelector(sel)
	if err != nil {
		return false
	}
	for _, p := range inv.podsOf(namespaceOf(svc), top) {
		if s.matches(p.labels()) {
			return true
		}
	}
	return false
}
