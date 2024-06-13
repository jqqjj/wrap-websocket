package wrap

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net"
	"sync"
)

type Server struct {
	*GroupServer
}

func NewServer() *Server {
	return &Server{
		GroupServer: NewGroup(),
	}
}

func (s *Server) Process(ctx context.Context, c *websocket.Conn) {
	var (
		err  error
		data []byte

		meta *sync.Map

		subCtx, subCancel = context.WithCancel(ctx)

		ip   = c.RemoteAddr().(*net.TCPAddr).IP.String()
		conn = NewConn(subCtx, c)
	)
	defer subCancel()

	go func() {
		<-subCtx.Done()
		conn.Close()
	}()

	for {
		var (
			req struct {
				Version string          `json:"version"`
				UUID    string          `json:"uuid"`
				Command string          `json:"command"`
				Payload json.RawMessage `json:"payload"`
			}
			respEntity = NewResponse(conn)
		)

		if _, data, err = conn.ReadMessage(); err != nil {
			return
		}
		if err = json.Unmarshal(data, &req); err != nil {
			respEntity.FailWithCodeAndMessage(404, "error parsing request")
			s.send(conn, &Request{}, respEntity)
			continue
		}

		if meta == nil {
			meta = &sync.Map{}
		}
		reqEntity := &Request{
			Version:  req.Version,
			UUID:     req.UUID,
			Command:  req.Command,
			Payload:  req.Payload,
			ClientIP: ip,
			meta:     meta,
		}

		//处理心跳包
		if req.Command == "ping" {
			respEntity.Success(nil)
			s.send(conn, reqEntity, respEntity)
			continue
		}

		handleEntity, ok := s.handles[reqEntity.Command]
		if !ok {
			respEntity.FailWithCodeAndMessage(404, "command not found")
			s.send(conn, reqEntity, respEntity)
			continue
		}

		next := handleEntity.handler
		for i := len(handleEntity.middlewares) - 1; i >= 0; i-- {
			nextFunc := handleEntity.middlewares[i]
			next = func(n HandleFunc) HandleFunc {
				return func(r *Request, w *Response) {
					nextFunc(n, r, w)
				}
			}(next)
		}

		next(reqEntity, respEntity)
		s.send(conn, reqEntity, respEntity)
	}
}

func (s *Server) send(conn *Conn, req *Request, resp *Response) {
	if !resp.filled {
		resp.SetResponseBody(ResponseBody{
			Code:    1,
			Message: "Server error",
			Data:    nil,
		})
	}

	conn.SendJSON(struct {
		UUID    string `json:"uuid"`
		Type    string `json:"type"`
		Command string `json:"command"`
		Body    any    `json:"body"`
	}{
		UUID:    req.UUID,
		Type:    "response",
		Command: req.Command,
		Body:    resp.GetResponseBody(),
	})
}
