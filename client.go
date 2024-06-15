package wrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"github.com/satori/go.uuid"
	"sync"
	"time"
)

type clientEntity struct {
	ClientId string `json:"client_id"`
	Version  string `json:"version"`
	UUID     string `json:"uuid"`
	Command  string `json:"command"`
	Payload  any    `json:"payload"`
}

type clientRequest struct {
	tryLeft       int
	body          clientEntity
	cancelWaiting context.CancelFunc
}

type Client struct {
	clientId string
	addr     string
	version  string
	timeout  time.Duration

	queueBuffer chan clientRequest
	querying    sync.Map
	pubSubPush  *PubSub[string, []byte]
	pubSubResp  *PubSub[string, []byte]

	onDialError func(*Client)
	onConnected func(*Client)
	onClosed    func(*Client)
}

func NewClient(clientId, addr, version string, timeoutDuration time.Duration) *Client {
	if timeoutDuration < time.Second {
		timeoutDuration = time.Second * 30
	}
	return &Client{
		clientId:    clientId,
		addr:        addr,
		version:     version,
		timeout:     timeoutDuration,
		queueBuffer: make(chan clientRequest, 100),
		pubSubPush:  NewPubSub[string, []byte](),
		pubSubResp:  NewPubSub[string, []byte](),
	}
}

func (c *Client) Run(ctx context.Context) {
	var (
		err   error
		fails int
		conn  *websocket.Conn

		dial = websocket.Dialer{
			HandshakeTimeout:  c.timeout,
			EnableCompression: false,
		}
		ticker = time.NewTicker(time.Millisecond)
	)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		ticker.Stop()

		if conn, _, err = dial.DialContext(ctx, c.addr, nil); err != nil {
			if callback := c.onDialError; callback != nil {
				callback(c)
			}
			fails++
			ticker.Reset(time.Duration(fails) * 2 * time.Second)
			continue
		}
		fails = 0

		//connected事件
		if callback := c.onConnected; callback != nil {
			callback(c)
		}

		c.loop(ctx, conn)

		//closed事件
		if callback := c.onClosed; callback != nil {
			callback(c)
		}

		ticker.Reset(time.Millisecond)
	}
}

func (c *Client) Send(ctx context.Context, command string, data any) ([]byte, error) {
	var (
		ch = make(chan []byte)

		reqId             = uuid.NewV4().String()
		subCtx, subCancel = context.WithTimeout(ctx, c.timeout)

		req = clientRequest{
			tryLeft: 1,
			body: clientEntity{
				ClientId: c.clientId,
				Version:  c.version,
				UUID:     reqId,
				Command:  command,
				Payload:  data,
			},
		}
	)
	defer subCancel()

	c.pubSubResp.Subscribe(subCtx, reqId, ch)

	select {
	case <-ctx.Done():
		return nil, errors.New("canceled")
	case <-subCtx.Done():
		select {
		case <-ctx.Done():
			return nil, errors.New("canceled")
		default:
			return nil, errors.New("timeout")
		}
	case c.queueBuffer <- req:
	}

	select {
	case <-ctx.Done():
		return nil, errors.New("canceled")
	case <-subCtx.Done():
		select {
		case <-ctx.Done():
			return nil, errors.New("canceled")
		default:
			return nil, errors.New("timeout")
		}
	case dataResp := <-ch:
		return dataResp, nil
	}
}

func (c *Client) SendWithoutResponse(command string, data any) bool {
	var (
		reqId = uuid.NewV4().String()
		req   = clientRequest{
			tryLeft: 1,
			body: clientEntity{
				ClientId: c.clientId,
				Version:  c.version,
				UUID:     reqId,
				Command:  command,
				Payload:  data,
			},
		}
	)

	select {
	case c.queueBuffer <- req:
		return true
	default:
		return false
	}
}

func (c *Client) SetDialErrCallback(f func(*Client)) {
	c.onDialError = f
}
func (c *Client) SetConnectedCallback(f func(*Client)) {
	c.onConnected = f
}
func (c *Client) SetClosedCallback(f func(*Client)) {
	c.onClosed = f
}

func (c *Client) loop(ctx context.Context, conn *websocket.Conn) {
	var (
		err  error
		data []byte

		wg sync.WaitGroup

		subCtx, subCancel = context.WithCancel(ctx)
	)
	defer wg.Wait()
	defer subCancel()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(c.timeout)
		defer ticker.Stop()
		defer conn.Close() //处理外部退出时关闭conn
		for {
			select {
			case <-subCtx.Done():
				return
			case <-ticker.C:
				ticker.Stop()
			}
			c.Send(subCtx, "ping", nil) //主动发心跳包
			ticker.Reset(c.timeout)
		}
	}()

	//建立单线程发送任务
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.runSending(subCtx, conn)
	}()

	//把正在发送中的队列重发
	histories := make([]clientRequest, 0)
	c.querying.Range(func(key, value any) bool {
		histories = append(histories, value.(clientRequest))
		c.querying.Delete(key)
		return true
	})
	for _, v := range histories {
		select {
		case <-subCtx.Done():
			c.querying.Store(v.body.UUID, v)
		case c.queueBuffer <- v:
		}
	}

	for {
		var resp struct {
			UUID    string          `json:"uuid"`
			Type    string          `json:"type"` //response push
			Command string          `json:"command"`
			Body    json.RawMessage `json:"body"`
		}
		if _, data, err = conn.ReadMessage(); err != nil {
			return
		}
		if err = json.Unmarshal(data, &resp); err != nil {
			return
		}
		if resp.Type == "push" {
			c.pubSubPush.Publish(resp.Command, resp.Body)
			continue
		}

		if val, ok := c.querying.LoadAndDelete(resp.UUID); ok {
			ent := val.(clientRequest)
			if ent.cancelWaiting != nil {
				ent.cancelWaiting() //快速释放等待超时的线程
			}
			//发布响应数据通知
			c.pubSubResp.Publish(resp.UUID, resp.Body)
		}
	}
}

func (c *Client) runSending(ctx context.Context, conn *websocket.Conn) {
	var (
		err error
		wg  sync.WaitGroup
		req clientRequest
	)
	defer wg.Wait()

	//接收请求向ws写入
	for {
		select {
		case <-ctx.Done():
			return
		case req = <-c.queueBuffer:
		}

		//没有重试机会的立即失败回调
		if req.tryLeft <= 0 {
			data, _ := json.Marshal(struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Data    any    `json:"data"`
			}{
				Code:    1,
				Message: "retries exhausted",
				Data:    nil,
			})
			c.pubSubResp.Publish(req.body.UUID, data)
			continue
		}

		//发送
		if err = conn.WriteJSON(req.body); err == nil {
			req.tryLeft-- //只有真正写入conn，才扣除重试次数
		}

		//保存到发送中队列
		subCtx, subCancel := context.WithCancel(context.Background())
		req.cancelWaiting = subCancel
		c.querying.Store(req.body.UUID, req)
		//设定定时器，超时继续重试
		wg.Add(1)
		go func(uuid string) {
			defer wg.Done()
			ticker := time.NewTimer(c.timeout)
			defer ticker.Stop()
			select {
			case <-ctx.Done(): //外部退出执行这里
				return
			case <-subCtx.Done(): //成功收到发送响应，会执行这里，为了快速释放等待资源
				return
			case <-ticker.C:
			}
			//执行重试
			if val, ok := c.querying.LoadAndDelete(uuid); ok {
				select {
				case <-ctx.Done(): //外部退出时，重回querying列表
					c.querying.Store(uuid, val.(clientRequest))
				case c.queueBuffer <- val.(clientRequest):
				}
			}
		}(req.body.UUID)
	}
}

func (c *Client) Subscribe(ctx context.Context, topic string, ch chan<- []byte) {
	c.pubSubPush.Subscribe(ctx, topic, ch)
}
