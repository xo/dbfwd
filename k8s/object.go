package k8s

import (
	"encoding/base64"
	"maps"
	"strconv"
)

// object is a Kubernetes object as kubectl get -o json prints it. The kinds
// that the backend reads differ too much for one struct, so it reads paths.
type object map[string]any

// get returns the value at path, or nil.
func (o object) get(path ...string) any {
	var v any = map[string]any(o)
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

// str returns the string at path, or empty.
func (o object) str(path ...string) string {
	switch v := o.get(path...).(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// boolean returns the bool at path, or false.
func (o object) boolean(path ...string) bool {
	b, _ := o.get(path...).(bool)
	return b
}

// num returns the number at path, or zero.
func (o object) num(path ...string) int {
	switch v := o.get(path...).(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

// list returns the objects in the array at path.
func (o object) list(path ...string) []object {
	a, _ := o.get(path...).([]any)
	out := make([]object, 0, len(a))
	for _, v := range a {
		if m, ok := v.(map[string]any); ok {
			out = append(out, object(m))
		}
	}
	return out
}

// strmap returns the map of strings at path.
func (o object) strmap(path ...string) map[string]string {
	m, _ := o.get(path...).(map[string]any)
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// name returns metadata.name.
func (o object) name() string {
	return o.str("metadata", "name")
}

// labels returns metadata.labels.
func (o object) labels() map[string]string {
	return o.strmap("metadata", "labels")
}

// owner is an owner reference.
type owner struct {
	kind  string
	group string
	name  string
}

// controller returns the owner reference that manages o, if it has one.
func (o object) controller() (owner, bool) {
	for _, r := range o.list("metadata", "ownerReferences") {
		if !r.boolean("controller") {
			continue
		}
		group := r.str("apiVersion")
		for i := range len(group) {
			if group[i] == '/' {
				group = group[:i]
				break
			}
		}
		return owner{kind: r.str("kind"), group: group, name: r.str("name")}, true
	}
	return owner{}, false
}

// secretData decodes the data of a Secret.
func secretData(o object) map[string]string {
	out := make(map[string]string)
	for k, v := range o.strmap("data") {
		if b, err := base64.StdEncoding.DecodeString(v); err == nil {
			out[k] = string(b)
		}
	}
	maps.Copy(out, o.strmap("stringData"))
	return out
}
