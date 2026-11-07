package k8s

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/xo/dbfwd"
)

// Step results.
const (
	resultMatch        = "match"
	resultNoMatch      = "no match"
	resultNotInstalled = "not installed"
	resultNotAllowed   = "not allowed"
	resultFailed       = "failed"
)

// lookup runs the lookup chain of D7 for one request.
type lookup struct {
	k     *kube
	req   request
	scope []dbfwd.Field
	steps []dbfwd.Step
	// forbidden is set when a step was not allowed.
	forbidden bool

	// installed holds the resources that the cluster has, or is nil when the
	// cluster did not say.
	installed map[string]bool
}

// getCR reads a custom resource, and skips the call when the cluster said that
// the resource is not installed.
func (l *lookup) getCR(ctx context.Context, resource, name string) (object, error) {
	if l.installed != nil && !l.installed[resource] {
		return nil, errNotInstalled
	}
	return l.k.get(ctx, resource, name)
}

// step records a step and turns its error into a result. It returns true when
// the lookup must go on to the next step, and the error that ends the lookup.
func (l *lookup) step(kind string, err error) (bool, error) {
	switch {
	case err == nil:
		l.steps = append(l.steps, dbfwd.Step{Kind: kind, Result: resultMatch})
		return false, nil
	case errors.Is(err, errNotFound):
		l.steps = append(l.steps, dbfwd.Step{Kind: kind, Result: resultNoMatch})
		return true, nil
	case errors.Is(err, errNotInstalled):
		l.steps = append(l.steps, dbfwd.Step{Kind: kind, Result: resultNotInstalled})
		return true, nil
	case errors.Is(err, dbfwd.ErrForbidden):
		l.steps = append(l.steps, dbfwd.Step{Kind: kind, Result: resultNotAllowed})
		l.forbidden = true
		return true, nil
	}
	l.steps = append(l.steps, dbfwd.Step{Kind: kind, Result: resultFailed})
	return false, err
}

// fail returns the error of a lookup that nothing matched.
func (l *lookup) fail(err error) error {
	return &dbfwd.LookupError{Name: l.req.name, Scope: l.scope, Steps: l.steps, Err: err}
}

// run tries each kind in order. The first match wins.
func (l *lookup) run(ctx context.Context) (*found, error) {
	ops := operators
	if op := operatorFor(l.req.typ); op != nil {
		ops = []*operator{op}
	}
	// step 1: a cluster CR of a supported operator
	for _, op := range ops {
		cr, err := l.getCR(ctx, op.resource, l.req.name)
		if next, err := l.step(op.name+" "+op.kind, err); err != nil {
			return nil, l.fail(err)
		} else if !next {
			return l.fromOperator(op, cr, "")
		}
	}
	// step 2: another resource of an operator that names a cluster
	for _, op := range ops {
		for _, c := range op.children {
			obj, err := l.getCR(ctx, c.resource, l.req.name)
			if next, err := l.step(op.name+" "+c.kind, err); err != nil {
				return nil, l.fail(err)
			} else if !next {
				f, err := l.fromChild(ctx, op, c, obj)
				if err != nil {
					return nil, l.fail(err)
				}
				return f, nil
			}
		}
	}
	type kindStep struct {
		kind string
		try  func(context.Context) (*found, error)
	}
	for _, s := range []kindStep{
		{"Service", l.service},
		{"Helm release", l.release},
		{"StatefulSet", l.statefulSet},
		{"Deployment", l.deployment},
		{"Pod", l.pod},
	} {
		f, err := s.try(ctx)
		next, err := l.step(s.kind, err)
		if err != nil {
			return nil, l.fail(err)
		}
		if !next {
			return f, nil
		}
	}
	if l.forbidden {
		return nil, l.fail(dbfwd.ErrForbidden)
	}
	return nil, l.fail(dbfwd.ErrNotFound)
}

// fromOperator builds what a lookup found from a cluster CR. via is the
// Service that matched, or empty when the CR matched.
func (l *lookup) fromOperator(op *operator, cr object, via string) (*found, error) {
	role := l.req.role
	if via != "" {
		implied := op.roleOf(cr.name(), via)
		if implied != "" && role != "" && implied != role {
			return nil, fmt.Errorf("using role %s with the service %s, which is %s: %w", role, via, implied, dbfwd.ErrRoleConflict)
		}
		if implied != "" {
			role = implied
		}
	}
	f, err := op.build(cr, role)
	if err != nil {
		return nil, err
	}
	if via != "" {
		f.forward = "service/" + via
	}
	f.target.Kind, f.target.Name = op.kind, cr.name()
	f.target.Owner = op.name + " " + op.kind + " " + cr.name()
	return f, nil
}

// fromChild builds what a lookup found from a resource that names a cluster,
// such as a CloudNativePG Pooler.
func (l *lookup) fromChild(ctx context.Context, op *operator, c *child, obj object) (*found, error) {
	name := c.cluster(obj)
	if name == "" {
		return nil, fmt.Errorf("reading the cluster of the %s %s: %w", c.kind, obj.name(), dbfwd.ErrNotFound)
	}
	cr, err := l.k.get(ctx, op.resource, name)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, fmt.Errorf("reading the cluster %s of the %s %s: %w", name, c.kind, obj.name(), dbfwd.ErrNotFound)
		}
		return nil, err
	}
	role := l.req.role
	if implied := c.role(obj); implied != "" {
		if role != "" && role != implied {
			return nil, fmt.Errorf("using role %s with the %s %s, which is %s: %w", role, c.kind, obj.name(), implied, dbfwd.ErrRoleConflict)
		}
		role = implied
	}
	f, err := op.build(cr, role)
	if err != nil {
		return nil, err
	}
	c.apply(obj, f)
	f.target.Kind, f.target.Name = c.kind, obj.name()
	f.target.Owner = op.name + " " + op.kind + " " + cr.name()
	return f, nil
}

// ownedBy is the operator that manages an object, and the cluster CR, or the
// child resource, that it reached.
type ownedBy struct {
	op       *operator
	cr       object
	child    *child
	childObj object
}

// owned follows the owner references of o up to a cluster CR or a child
// resource of an operator. It returns nil when no operator manages o.
func (l *lookup) owned(ctx context.Context, o object) (*ownedBy, error) {
	for range 4 {
		ref, ok := o.controller()
		if !ok {
			return nil, nil
		}
		for _, op := range operators {
			if op.owns(ref.kind, ref.group) {
				cr, err := l.k.get(ctx, op.resource, ref.name)
				return &ownedBy{op: op, cr: cr}, err
			}
			if c := op.childOf(ref.kind, ref.group); c != nil {
				obj, err := l.k.get(ctx, c.resource, ref.name)
				return &ownedBy{op: op, child: c, childObj: obj}, err
			}
		}
		if next, ok, err := l.between(ctx, ref); err != nil {
			return nil, err
		} else if ok {
			o = next
			continue
		}
		if group, ok := perconaKinds[ref.kind]; ok && group == ref.group {
			cr, err := l.k.get(ctx, everest.resource, ref.name)
			return &ownedBy{op: everest, cr: cr}, err
		}
		var resource string
		switch ref.kind {
		case "StatefulSet", "ReplicaSet", "Deployment":
			resource = strings.ToLower(ref.kind) + ".apps"
		default:
			return nil, nil
		}
		next, err := l.k.get(ctx, resource, ref.name)
		if err != nil {
			return nil, err
		}
		o = next
	}
	return nil, nil
}

// between reads an object that sits between an object and the cluster CR in
// the owner chain of an operator, such as a ScyllaDBDatacenter.
func (l *lookup) between(ctx context.Context, ref owner) (object, bool, error) {
	for _, op := range operators {
		if resource, ok := op.between[ref.kind]; ok && slices.Contains(op.groups, ref.group) {
			o, err := l.k.get(ctx, resource, ref.name)
			return o, err == nil, err
		}
	}
	return nil, false, nil
}

// byLabels returns the operator and the cluster CR that the labels of an
// object name, for an operator that sets no owner reference by default.
func (l *lookup) byLabels(ctx context.Context, o object) (*ownedBy, error) {
	for _, op := range operators {
		if op.fromLabels == nil {
			continue
		}
		if name := op.fromLabels(o.labels()); name != "" {
			cr, err := l.getCR(ctx, op.resource, name)
			if err != nil {
				return nil, err
			}
			return &ownedBy{op: op, cr: cr}, nil
		}
	}
	return nil, nil
}

// fromOwner builds what a lookup found from the operator that manages an
// object. via is the Service that matched, or empty.
func (l *lookup) fromOwner(ctx context.Context, by *ownedBy, via string) (*found, error) {
	if by.child != nil {
		return l.fromChild(ctx, by.op, by.child, by.childObj)
	}
	return l.fromOperator(by.op, by.cr, via)
}

// service is step 3: a Service with the name.
func (l *lookup) service(ctx context.Context) (*found, error) {
	svc, err := l.k.get(ctx, "service", l.req.name)
	if err != nil {
		return nil, err
	}
	return l.fromService(ctx, svc)
}

// fromService builds what a lookup found from a Service.
func (l *lookup) fromService(ctx context.Context, svc object) (*found, error) {
	name := svc.name()
	if svc.str("spec", "type") == "ExternalName" {
		return nil, fmt.Errorf("forwarding to the service %s, which points outside the cluster: %w", name, dbfwd.ErrUnsupported)
	}
	by, err := l.owned(ctx, svc)
	if err == nil && by == nil {
		by, err = l.byLabels(ctx, svc)
	}
	if err != nil {
		return nil, err
	} else if by != nil {
		f, err := l.fromOwner(ctx, by, name)
		if err != nil {
			return nil, err
		}
		f.target.Kind, f.target.Name = "Service", name
		return f, nil
	}
	selector := svc.strmap("spec", "selector")
	if len(selector) == 0 {
		return nil, fmt.Errorf("finding the pods of the service %s, which has no selector: %w", name, dbfwd.ErrUnsupported)
	}
	pods, err := l.k.list(ctx, "pods", selectorString(selector))
	if err != nil {
		return nil, err
	}
	pod, ok := runningPod(pods)
	if !ok {
		return nil, fmt.Errorf("finding a running pod of the service %s: %w", name, dbfwd.ErrNotFound)
	}
	ports := svc.list("spec", "ports")
	if len(ports) == 0 {
		return nil, fmt.Errorf("reading the ports of the service %s: %w", name, dbfwd.ErrNotFound)
	}
	port, target := pickServicePort(ports, pod)
	f, err := fromPodSpec(object(asMap(pod.get("spec"))), target, pod.labels()["app.kubernetes.io/name"], l.req.typ)
	if err != nil {
		return nil, err
	}
	f.forward, f.port = "service/"+name, port
	f.target.Kind, f.target.Name = "Service", name
	return f, nil
}

// pickServicePort returns the port of a Service that reaches the database,
// and the port it targets. A Service with one port uses it.
func pickServicePort(ports []object, pod object) (int, string) {
	if len(ports) == 1 {
		return ports[0].num("port"), ports[0].str("targetPort")
	}
	spec := object(asMap(pod.get("spec")))
	c, ok := pickContainer(spec, "")
	for _, p := range ports {
		target := p.str("targetPort")
		n, err := strconv.Atoi(target)
		if ok && ((err == nil && hasPort(c, n)) || (err != nil && containerPort(c, target) != 0)) {
			return p.num("port"), target
		}
	}
	return ports[0].num("port"), ports[0].str("targetPort")
}

// release is step 4: the database Service of a Helm release.
func (l *lookup) release(ctx context.Context) (*found, error) {
	svcs, err := l.k.list(ctx, "services", "app.kubernetes.io/instance="+l.req.name)
	if err != nil {
		return nil, err
	}
	var candidates []object
	for _, s := range svcs {
		if s.str("spec", "clusterIP") == "None" || len(s.list("spec", "ports")) == 0 {
			continue
		}
		candidates = append(candidates, s)
	}
	switch len(candidates) {
	case 0:
		return nil, errNotFound
	case 1:
		return l.fromService(ctx, candidates[0])
	}
	names := make([]string, 0, len(candidates))
	for _, s := range candidates {
		names = append(names, s.name())
	}
	sort.Strings(names)
	return nil, fmt.Errorf("choosing the service of the release %s among %s: %w", l.req.name, strings.Join(names, ", "), dbfwd.ErrAmbiguous)
}

// statefulSet is step 5: a StatefulSet with the name. It forwards to the pod
// with ordinal 0.
func (l *lookup) statefulSet(ctx context.Context) (*found, error) {
	sts, err := l.k.get(ctx, "statefulset.apps", l.req.name)
	if err != nil {
		return nil, err
	}
	pod, err := l.k.get(ctx, "pod", l.req.name+"-0")
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, fmt.Errorf("finding the pod %s-0 of the statefulset: %w", l.req.name, dbfwd.ErrNotFound)
		}
		return nil, err
	}
	f, err := l.fromPod(ctx, pod)
	if err != nil {
		return nil, err
	}
	f.target.Kind, f.target.Name = "StatefulSet", sts.name()
	return f, nil
}

// deployment is step 5: a Deployment with the name. kubectl picks its pod.
func (l *lookup) deployment(ctx context.Context) (*found, error) {
	d, err := l.k.get(ctx, "deployment.apps", l.req.name)
	if err != nil {
		return nil, err
	}
	spec := object(asMap(d.get("spec", "template", "spec")))
	f, err := fromPodSpec(spec, "", d.str("spec", "template", "metadata", "labels", "app.kubernetes.io/name"), l.req.typ)
	if err != nil {
		return nil, err
	}
	f.forward = "deployment/" + d.name()
	f.target.Kind, f.target.Name = "Deployment", d.name()
	return f, nil
}

// pod is step 6: a Pod with the name.
func (l *lookup) pod(ctx context.Context) (*found, error) {
	pod, err := l.k.get(ctx, "pod", l.req.name)
	if err != nil {
		return nil, err
	}
	return l.fromPod(ctx, pod)
}

// fromPod builds what a lookup found from a Pod.
func (l *lookup) fromPod(ctx context.Context, pod object) (*found, error) {
	name := pod.name()
	by, err := l.owned(ctx, pod)
	if err == nil && by == nil {
		by, err = l.byLabels(ctx, pod)
	}
	if err != nil {
		return nil, err
	} else if by != nil {
		f, err := l.fromOwner(ctx, by, "")
		if err != nil {
			return nil, err
		}
		// the port of the operator is that of its Service, and the pod can
		// listen on another, as Solr does behind port 80
		if svc, ok := strings.CutPrefix(f.forward, "service/"); ok {
			if s, err := l.k.get(ctx, "service", svc); err == nil {
				f.port = podPort(s, pod, f.port)
			}
		}
		f.forward = "pod/" + name
		f.target.Kind, f.target.Name = "Pod", name
		return f, nil
	}
	spec := object(asMap(pod.get("spec")))
	f, err := fromPodSpec(spec, "", pod.labels()["app.kubernetes.io/name"], l.req.typ)
	if err != nil {
		return nil, err
	}
	f.forward = "pod/" + name
	f.target.Kind, f.target.Name = "Pod", name
	return f, nil
}

// podPort returns the port of a pod that a port of a Service reaches: its
// target port, by number or by the name of a container port. It returns port
// when the Service does not say.
func podPort(svc, pod object, port int) int {
	for _, p := range svc.list("spec", "ports") {
		if p.num("port") != port {
			continue
		}
		target := p.str("targetPort")
		if n, err := strconv.Atoi(target); err == nil && n != 0 {
			return n
		}
		if target != "" {
			for _, c := range object(asMap(pod.get("spec"))).list("containers") {
				if n := containerPort(c, target); n != 0 {
					return n
				}
			}
		}
	}
	return port
}

// asMap returns v as a map, or an empty one.
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// runningPod returns the first pod that runs, by name.
func runningPod(pods []object) (object, bool) {
	sort.Slice(pods, func(i, j int) bool { return pods[i].name() < pods[j].name() })
	for _, p := range pods {
		if p.str("status", "phase") == "Running" {
			return p, true
		}
	}
	return nil, false
}

// selectorString builds a label selector from a map, in a stable order.
func selectorString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}
