package main

import (
	"context"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/jqqjj/wrap-websocket"
	"net/http"
)

func main() {
	srv := wsServer()
	ctx := context.Background()

	http.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		var (
			err error
			c   *websocket.Conn

			u = websocket.Upgrader{
				ReadBufferSize:  1024 * 4,
				WriteBufferSize: 1024 * 4,
				CheckOrigin:     func(*http.Request) bool { return true },
			}
		)

		if c, err = u.Upgrade(writer, request, nil); err != nil {
			writer.Write([]byte("error when upgrade to websocket"))
			return
		}
		defer c.Close()

		srv.Process(ctx, wrap.NewConn(ctx, c))
	})

	http.ListenAndServe("0.0.0.0:8089", nil)
}

func wsServer() *wrap.Server {
	s := wrap.NewServer()

	s.Use(func(next wrap.HandleFunc, r *wrap.Request, w *wrap.Response) {
		if _, ok := r.Get("conn"); !ok {
			fmt.Println("check once")
			r.Set("conn", w.GetConn())
		}
		fmt.Println("s1")
		next(r, w)
		//body := w.GetResponseBody()
		//body.Message = "s1 message"
		//w.SetResponseBody(body)
		fmt.Println("s1 final")
	}, func(next wrap.HandleFunc, r *wrap.Request, w *wrap.Response) {
		fmt.Println("s2")
		next(r, w)
		fmt.Println("s2 final")
	})

	g := s.Group("api/", func(next wrap.HandleFunc, r *wrap.Request, w *wrap.Response) {
		fmt.Println("gm1")
		next(r, w)
	}, func(next wrap.HandleFunc, r *wrap.Request, w *wrap.Response) {
		fmt.Println("gm2")
		//w.FailWithMessage("error: gm2")
		next(r, w)
	})

	g.SetHandle("sea", func(r *wrap.Request, w *wrap.Response) {
		fmt.Printf("sea data:%s, command:%s, ip:%s, uuid:%s\n", r.Payload, r.Command, r.ClientIP, r.UUID)
		w.Success("sea")
	})

	g.Use(func(next wrap.HandleFunc, r *wrap.Request, w *wrap.Response) {
		fmt.Println("g1")
		next(r, w)
	}, func(next wrap.HandleFunc, r *wrap.Request, w *wrap.Response) {
		fmt.Println("g2")
		next(r, w)
	})

	subG := g.Group("pd/")

	subG.SetHandle("test", func(r *wrap.Request, w *wrap.Response) {
		//panic("panic  aaa")
		fmt.Printf("payload:%s, command:%s, ip:%s, uuid:%s\n", r.Payload, r.Command, r.ClientIP, r.UUID)
		w.Success("test")
		p := wrap.NewPush(r.MustGet("conn").(*wrap.Conn))
		p.SendJSON("haha", struct {
			Token string `json:"token"`
		}{Token: "token123456"})
	}, func(next wrap.HandleFunc, r *wrap.Request, w *wrap.Response) {
		fmt.Println("c1")
		next(r, w)
		fmt.Println("c1 final")
	})

	return s
}
