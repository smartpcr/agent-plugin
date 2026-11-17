package cordis

// chainMap is a map whose lookups fall through to its parent while writes stay local:
// the Go form of upstream's Object.create(parent) chains for isolate and intercept
// tables. A derived map is never written after it is published; only the root isolate
// map gains keys later, and that happens under the tree lock.
type chainMap[V any] struct {
	parent *chainMap[V]
	own    map[string]V
}

func newChainMap[V any](parent *chainMap[V]) *chainMap[V] {
	return &chainMap[V]{parent: parent, own: map[string]V{}}
}

func (m *chainMap[V]) get(key string) (V, bool) {
	for cursor := m; cursor != nil; cursor = cursor.parent {
		if value, ok := cursor.own[key]; ok {
			return value, true
		}
	}
	var zero V
	return zero, false
}

func (m *chainMap[V]) hasOwn(key string) bool {
	_, ok := m.own[key]
	return ok
}

func (m *chainMap[V]) set(key string, value V) { m.own[key] = value }

// chain returns every value stored for key, nearest first.
func (m *chainMap[V]) chain(key string) []V {
	var values []V
	for cursor := m; cursor != nil; cursor = cursor.parent {
		if value, ok := cursor.own[key]; ok {
			values = append(values, value)
		}
	}
	return values
}
