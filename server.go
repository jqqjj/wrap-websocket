package wrap

import (
	"context"
	"encoding/json"
	"net"
	"sync"
)

type Server struct {
	IpResolver func() string
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
		ip   string
		meta *sync.Map

		subCtx, subCancel = context.WithCancel(ctx)
	)
	defer subCancel()

	go func() {
		<-subCtx.Done()
		conn.Close()
	}()

	if s.IpResolver != nil {
		ip = s.IpResolver()
	} else {
		ip = conn.RemoteAddr().(*net.TCPAddr).IP.String()
	}

	for {
		var (
			req struct {
				ClientId string          `json:"client_id"`
				Version  string          `json:"version"`
				UUID     string          `json:"uuid"`
				Command  string          `json:"command"`
				Payload  json.RawMessage `json:"payload"`
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
			ClientId: req.ClientId,
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
