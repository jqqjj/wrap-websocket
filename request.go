package wrap

import (
	"encoding/json"
	"sync"
)

type Request struct {
	ClientId  string
	Version   string
	RequestId string
	Command   string
	Payload   json.RawMessage
	ClientIP  string

	meta *sync.Map
}

func (r *Request) Set(key string, value any) {
	r.meta.Store(key, value)
}

func (r *Request) Delete(key string) {
	r.meta.Delete(key)
}

func (r *Request) Get(key string) (any, bool) {
	return r.meta.Load(key)
}

func (r *Request) Has(key string) bool {
	_, ok := r.meta.Load(key)
	return ok
}

func (r *Request) MustGet(key string) any {
	if o, ok := r.Get(key); ok {
		return o
	}
	panic("object not found: " + key)
}
