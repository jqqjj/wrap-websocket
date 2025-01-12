package main

import (
	"context"
	"fmt"
	"github.com/jqqjj/wrap-websocket"
	uuid "github.com/satori/go.uuid"
	"log"
	"time"
)

func main() {
	var (
		ch          = make(chan []byte)
		uri         = fmt.Sprintf("ws://%s/", "localhost:8089")
		ctx, cancel = context.WithTimeout(context.Background(), time.Minute*1)
	)
	defer cancel()

	client := wrap.NewClient(uuid.NewV4().String(), uri, "0.1", time.Second*15)
	go client.Run(ctx)

	client.Subscribe(ctx, "haha", ch)

	go func() {
		for v := range ch {
			log.Println("收到推送", string(v))
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
