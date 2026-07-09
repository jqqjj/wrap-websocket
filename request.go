package wrap

import (
	"encoding/json"
	"net"
	"sync"
)

type Request struct {
	ClientId   string
	Version    string
	RequestId  string
	Command    string
	Payload    json.RawMessage
	ClientAddr net.Addr

	sessionMeta *sync.Map
	flashMeta   *sync.Map
}

func (r *Request) Set(key string, value any, flash ...bool) {
	if len(flash) > 0 && flash[0] {
		r.flashMeta.Store(key, value)
	} else {
		r.sessionMeta.Store(key, value)
	}
}

func (r *Request) Delete(key string, flash ...bool) {
	if len(flash) > 0 && flash[0] {
		r.flashMeta.Delete(key)
	} else {
		r.sessionMeta.Delete(key)
	}
}

func (r *Request) Get(key string, flash ...bool) (any, bool) {
	if len(flash) > 0 && flash[0] {
		return r.flashMeta.Load(key)
	} else {
		return r.sessionMeta.Load(key)
	}
}

func (r *Request) Has(key string, flash ...bool) (ok bool) {
	if len(flash) > 0 && flash[0] {
		_, ok = r.flashMeta.Load(key)
	} else {
		_, ok = r.sessionMeta.Load(key)
	}
	return
}

func (r *Request) MustGet(key string, flash ...bool) any {
	if o, ok := r.Get(key, flash...); ok {
		return o
	}
	panic("object not found: " + key)
}
