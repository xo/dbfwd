package k8s

import (
	"context"
	"errors"
	"fmt"
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
)

// lookup runs the lookup chain of D7 for one request.
type lookup struct {
	k     *kube
	req   request
	scope []dbfwd.Field
	steps []dbfwd.Step
	// forbidden is set when a step was not allowed.
	forbidden bool
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
		cr, err := l.k.get(ctx, op.resource, l.req.name)
		if next, err := l.step(op.name+" "+op.kind, err); err != nil {
			return nil, l.fail(err)
		} else if !next {
			return l.fromOperator(op, cr, "")
		}
	}
	// step 2, the other resources of an operator, waits until their owner
	// chains are read from the upstream source (docs/PLAN.md)
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

// owned follows the owner references of o up to a cluster CR. It returns the
// operator and the CR, or nil when no operator manages o.
func (l *lookup) owned(ctx context.Context, o object) (*operator, object, error) {
	for range 4 {
		ref, ok := o.controller()
		if !ok {
			return nil, nil, nil
		}
		for _, op := range operators {
			if op.owns(ref.kind, ref.group) {
				cr, err := l.k.get(ctx, op.resource, ref.name)
				return op, cr, err
			}
		}
		if group, ok := perconaKinds[ref.kind]; ok && group == ref.group {
			cr, err := l.k.get(ctx, everest.resource, ref.name)
			return everest, cr, err
		}
		var resource string
		switch ref.kind {
		case "StatefulSet", "ReplicaSet", "Deployment":
			resource = strings.ToLower(ref.kind) + ".apps"
		default:
			return nil, nil, nil
		}
		next, err := l.k.get(ctx, resource, ref.name)
		if err != nil {
			return nil, nil, err
		}
		o = next
	}
	return nil, nil, nil
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
	if op, cr, err := l.owned(ctx, svc); err != nil {
		return nil, err
	} else if op != nil {
		f, err := l.fromOperator(op, cr, name)
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
	if op, cr, err := l.owned(ctx, pod); err != nil {
		return nil, err
	} else if op != nil {
		f, err := l.fromOperator(op, cr, "")
		if err != nil {
			return nil, err
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
