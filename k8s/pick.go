package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/xo/dbfwd"
)

// picker picks the pod that a forward goes to, the first time and again on
// each reconnect (D16). dbfwd never leaves the choice of the pod to kubectl,
// so that it knows which pod to watch.
type picker struct {
	k *kube

	// forward is what the lookup found: pod/<name>, service/<name>,
	// deployment/<name>, or empty when only pods selects.
	forward string

	// pods is the selector of the pods of the role of an operator, or empty.
	pods string

	// port is the port that the lookup found, of the Service or of the pod.
	port int
}

// picked is a pod to forward to.
type picked struct {
	// pod is the name of the pod, and port the port of the pod.
	pod  string
	port int

	// selector is the selector that the pod must keep matching, or nil when
	// the URL named the pod itself.
	selector selector

	// ready is whether the pod was ready when it was picked. Only a pod that
	// was ready reconnects when it stops being ready.
	ready bool
}

// pick picks a pod for the target. It prefers a ready pod, and takes a pod
// that runs when none is ready, as kubectl port-forward does.
func (p *picker) pick(ctx context.Context) (picked, error) {
	kind, name, _ := strings.Cut(p.forward, "/")
	switch kind {
	case "pod":
		pod, err := p.k.get(ctx, "pod", name)
		if err != nil {
			return picked{}, err
		}
		if !running(pod) {
			return picked{}, fmt.Errorf("waiting for the pod %s to run: %w", name, dbfwd.ErrNotFound)
		}
		return picked{pod: name, port: p.port, ready: ready(pod)}, nil
	case "service":
		svc, err := p.k.get(ctx, "service", name)
		switch {
		case errors.Is(err, errNotFound) && p.pods != "":
			// a Service that the CR did not ask for, such as that of TiDB
			return p.fromSelector(ctx, p.pods, nil)
		case err != nil:
			return picked{}, fmt.Errorf("reading the service %s: %w", name, err)
		case svc.str("spec", "type") == "ExternalName":
			return picked{}, fmt.Errorf("forwarding to the service %s, which points outside the cluster: %w", name, dbfwd.ErrUnsupported)
		}
		sel := selectorString(svc.strmap("spec", "selector"))
		if sel == "" {
			// an operator writes the endpoints of this Service, and the role
			// of its pods selects them
			if p.pods == "" {
				return picked{}, fmt.Errorf("forwarding to the service %s, which has no selector: %w", name, dbfwd.ErrUnsupported)
			}
			sel = p.pods
		}
		return p.fromSelector(ctx, sel, svc)
	case "deployment":
		d, err := p.k.get(ctx, "deployment.apps", name)
		if err != nil {
			return picked{}, fmt.Errorf("reading the deployment %s: %w", name, err)
		}
		return p.fromSelector(ctx, selectorString(d.strmap("spec", "selector", "matchLabels")), nil)
	case "":
		if p.pods != "" {
			return p.fromSelector(ctx, p.pods, nil)
		}
	}
	return picked{}, fmt.Errorf("forwarding to %q: %w", p.forward, dbfwd.ErrUnsupported)
}

// fromSelector picks a pod that the selector selects. svc is the Service
// whose port the lookup found, which turns it into the port of the pod, or
// nil when the port is already that of the pod.
func (p *picker) fromSelector(ctx context.Context, sel string, svc object) (picked, error) {
	s, err := parseSelector(sel)
	if err != nil {
		return picked{}, err
	}
	pods, err := p.k.list(ctx, "pods", sel)
	if err != nil {
		return picked{}, err
	}
	pod, ok := choosePod(pods)
	if !ok {
		return picked{}, fmt.Errorf("finding a running pod for %s: %w", sel, dbfwd.ErrNotFound)
	}
	port := p.port
	if svc != nil {
		port = podPort(svc, pod, port)
	}
	return picked{pod: pod.name(), port: port, selector: s, ready: ready(pod)}, nil
}

// running reports whether a pod runs and is not being deleted.
func running(pod object) bool {
	return pod.str("status", "phase") == "Running" && pod.get("metadata", "deletionTimestamp") == nil
}

// ready reports whether a pod runs and is ready.
func ready(pod object) bool {
	if !running(pod) {
		return false
	}
	for _, c := range pod.list("status", "conditions") {
		if c.str("type") == "Ready" {
			return c.str("status") == "True"
		}
	}
	// a pod with no conditions, as in a test, counts as ready when it runs
	return true
}

// choosePod returns the first ready pod by name, or else the first pod that
// runs. An operator can keep a pod unready for a while that still serves,
// such as the HAProxy of Percona XtraDB Cluster.
func choosePod(pods []object) (object, bool) {
	sort.Slice(pods, func(i, j int) bool { return pods[i].name() < pods[j].name() })
	for _, p := range pods {
		if ready(p) {
			return p, true
		}
	}
	for _, p := range pods {
		if running(p) {
			return p, true
		}
	}
	return nil, false
}

// selector is a label selector of the forms that dbfwd writes: key=value and
// key in (a,b), joined by commas.
type selector []requirement

// requirement is one term of a selector.
type requirement struct {
	key    string
	values []string
}

// parseSelector reads a selector of the forms that dbfwd writes.
func parseSelector(s string) (selector, error) {
	var sel selector
	for s = strings.TrimSpace(s); s != ""; {
		var term string
		if i := strings.Index(s, " in ("); i >= 0 && (strings.IndexByte(s, ',') == -1 || i < strings.IndexByte(s, ',')) {
			end := strings.IndexByte(s, ')')
			if end < 0 {
				return nil, fmt.Errorf("reading the selector %q: %w", s, dbfwd.ErrBadURL)
			}
			term, s = s[:end+1], strings.TrimPrefix(strings.TrimSpace(s[end+1:]), ",")
			key, list, _ := strings.Cut(term, " in (")
			var values []string
			for v := range strings.SplitSeq(strings.TrimSuffix(list, ")"), ",") {
				values = append(values, strings.TrimSpace(v))
			}
			sel = append(sel, requirement{key: strings.TrimSpace(key), values: values})
			continue
		}
		term, s, _ = strings.Cut(s, ",")
		key, value, ok := strings.Cut(term, "=")
		if !ok {
			return nil, fmt.Errorf("reading the selector term %q: %w", term, dbfwd.ErrBadURL)
		}
		sel = append(sel, requirement{key: strings.TrimSpace(key), values: []string{strings.TrimSpace(value)}})
		s = strings.TrimSpace(s)
	}
	return sel, nil
}

// matches reports whether labels match every term of the selector.
func (sel selector) matches(labels map[string]string) bool {
	for _, r := range sel {
		v, ok := labels[r.key]
		if !ok {
			return false
		}
		found := false
		for _, want := range r.values {
			if v == want {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
