package wrap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/gorilla/websocket"
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
				_ = c.flush()
			}
		}
	}()
	return c
}

func (c *Conn) flush() error {
	c.mux.Lock()
	arr := make([]any, 0, len(c.queueJson))
	arr = append(arr, c.queueJson...)
	c.queueJson = c.queueJson[0:0]
	c.mux.Unlock()

	c.writeMux.Lock()
	defer c.writeMux.Unlock()
	for i, v := range arr {
		if err := c.Conn.WriteJSON(v); err != nil {
			c.mux.Lock()
			remaining := append([]any{v}, arr[i+1:]...)
			c.queueJson = append(remaining, c.queueJson...)
			c.mux.Unlock()
			return err
		}
	}
	return nil
}

func (c *Conn) AddClosedCallback(f func()) {
	c.closedCallback = append(c.closedCallback, f)
}

func (c *Conn) Close() error {
	for _, f := range c.closedCallback {
		f()
	}
	return errors.Join(c.flush(), c.Conn.Close())
}

func (c *Conn) ID() string {
	return fmt.Sprintf("%p", c.Conn.NetConn())
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
