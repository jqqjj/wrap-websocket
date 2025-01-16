package wrap

import (
	"context"
	"github.com/gorilla/websocket"
	"net"
	"sync"
)

type Conn struct {
	*websocket.Conn

	mux      sync.Mutex
	writeMux sync.Mutex

	AddrResolver   func(*Conn) net.Addr
	closedCallback []func()

	queueJson []any
	trigger   chan struct{}
}

func NewConn(ctx context.Context, conn *websocket.Conn) *Conn {
	c := &Conn{Conn: conn, queueJson: make([]any, 0), trigger: make(chan struct{}, 1)}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.trigger:
				c.flush()
			}
		}
	}()
	return c
}

func (c *Conn) flush() {
	c.mux.Lock()
	arr := make([]any, 0, len(c.queueJson))
	arr = append(arr, c.queueJson...)
	c.queueJson = c.queueJson[0:0]
	c.mux.Unlock()

	c.writeMux.Lock()
	defer c.writeMux.Unlock()
	for _, v := range arr {
		c.Conn.WriteJSON(v)
	}
}

func (c *Conn) AddClosedCallback(f func()) {
	c.closedCallback = append(c.closedCallback, f)
}

func (c *Conn) Close() error {
	for _, f := range c.closedCallback {
		f()
	}
	c.flush()
	return c.Conn.Close()
}

func (c *Conn) ReadMessage() (messageType int, p []byte, err error) {
	return c.Conn.ReadMessage()
}

func (c *Conn) sendEntity(v any) {
	c.mux.Lock()
	defer c.mux.Unlock()

	c.queueJson = append(c.queueJson, v)
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}
