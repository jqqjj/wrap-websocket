package wrap

import (
	"context"
	"encoding/json"
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

func (s *Server) Process(ctx context.Context, conn *Conn) {
	var (
		err  error
		data []byte
		addr net.Addr
		meta *sync.Map

		subCtx, subCancel = context.WithCancel(ctx)
	)
	defer subCancel()

	go func() {
		<-subCtx.Done()
		conn.Close()
	}()

	if conn.AddrResolver != nil {
		addr = conn.AddrResolver(conn)
	} else {
		addr = conn.RemoteAddr()
	}

	for {
		var (
			req struct {
				ClientId  string          `json:"client_id"`
				Version   string          `json:"version"`
				RequestId string          `json:"request_id"`
				Command   string          `json:"command"`
				Payload   json.RawMessage `json:"payload"`
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
			ClientId:   req.ClientId,
			Version:    req.Version,
			RequestId:  req.RequestId,
			Command:    req.Command,
			Payload:    req.Payload,
			ClientAddr: addr,
			meta:       meta,
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

		if respEntity.closed {
			conn.Close()
			break
		}
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

	conn.sendEntity(struct {
		RequestId string `json:"request_id"`
		Type      string `json:"type"`
		Command   string `json:"command"`
		Body      any    `json:"body"`
	}{
		RequestId: req.RequestId,
		Type:      "response",
		Command:   req.Command,
		Body:      resp.GetResponseBody(),
	})
}
