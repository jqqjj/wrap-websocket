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
		ch          = make(chan wrap.PubSubChan[Event, []byte])
		uri         = fmt.Sprintf("ws://%s/", "localhost:8089")
		ctx, cancel = context.WithTimeout(context.Background(), time.Minute*1)
	)
	defer cancel()

	client := wrap.NewClient[Event](uuid.NewV4().String(), uri, "0.1", time.Second*15)
	go client.Run(ctx)

	client.Subscribe(ctx, e1, ch)

	go func() {
		for v := range ch {
			log.Println("收到推送", string(v.Data))
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
