package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jqqjj/wrap-websocket"
	uuid "github.com/satori/go.uuid"
)

type Event struct {
	s string
}

func (e Event) String() string {
	return e.s
}

var e1 Event

func main() {
	var (
		uri         = fmt.Sprintf("ws://%s/", "localhost:8089")
		ctx, cancel = context.WithTimeout(context.Background(), time.Second*10)
	)
	defer cancel()

	client := wrap.NewClient[Event](uuid.NewV4().String(), uri, "0.1", time.Second*15)
	go client.Run(ctx)

	ch := client.Subscribe(ctx, e1)

	go func() {
		for v := range ch {
			select {
			case <-v.Ctx.Done():
				log.Println("结束")
				return
			default:
			}
			log.Println("收到推送", string(v.Data))
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Println("结束")
				return
			case <-time.After(time.Second * 2):
			}
			client.Publish(e1, []byte("hello world"))
		}
	}()

	for {
		data, err := client.Send(ctx, "api/pd/test", struct {
			Username string `json:"username"`
		}{
			Username: "admin",
		})

		if err != nil {
			log.Println("错误", err)
			time.Sleep(time.Second)
			continue
		}

		log.Println("收到", data.Code, data.Message, string(data.Data))

		time.Sleep(time.Second)
	}
}
