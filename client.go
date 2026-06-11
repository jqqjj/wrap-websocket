package wrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/satori/go.uuid"
)

type clientEntity struct {
	ClientId  string `json:"client_id"`
	Version   string `json:"version"`
	RequestId string `json:"request_id"`
	Command   string `json:"command"`
	Payload   any    `json:"payload"`
}

type clientRequest struct {
	tryLeft       int
	body          clientEntity
	cancelWaiting context.CancelFunc
}

type ResponseEntity struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type TopicString interface {
	fmt.Stringer
	comparable
}

type Client[T TopicString] struct {
	clientId string
	addr     string
	version  string
	timeout  time.Duration

	notify     chan struct{}
	queueMux   sync.Mutex
	queueItems []*clientRequest
	querying   sync.Map
	pubSub     *PubSub[T, []byte]
	pubSubReq  *PubSub[string, []byte]

	onDialError func(*Client[T])
	onConnected func(*Client[T])
	onClosed    func(*Client[T])
}

func NewClient[T TopicString](clientId, addr, version string, timeoutDuration time.Duration) *Client[T] {
	if timeoutDuration < time.Second {
		timeoutDuration = time.Second * 30
	}
	return &Client[T]{
		clientId:   clientId,
		addr:       addr,
		version:    version,
		timeout:    timeoutDuration,
		notify:     make(chan struct{}, 1),
		queueItems: make([]*clientRequest, 0),
		pubSub:     NewPubSub[T, []byte](),
		pubSubReq:  NewPubSub[string, []byte](),
	}
}

func (c *Client[T]) Run(ctx context.Context) {
	var (
		err   error
		fails int
		conn  *websocket.Conn

		dial = websocket.Dialer{
			HandshakeTimeout:  c.timeout,
			EnableCompression: false,
		}
		timer = time.NewTimer(time.Millisecond)
	)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if conn, _, err = dial.DialContext(ctx, c.addr, nil); err != nil {
			if callback := c.onDialError; callback != nil {
				callback(c)
			}
			fails++
			timer.Reset(time.Duration(fails) * 2 * time.Second)
			continue
		}
		fails = 0

		c.loop(ctx, conn)

		timer.Reset(time.Millisecond)
	}
}

func (c *Client[T]) Send(ctx context.Context, command string, data any) (*ResponseEntity, error) {
	return c.SendTries(ctx, 1, command, data)
}

func (c *Client[T]) SendTries(ctx context.Context, tries int, command string, data any) (*ResponseEntity, error) {
	return c.sendTries(ctx, tries, command, data)
}

func (c *Client[T]) sendTries(ctx context.Context, tries int, command string, data any) (*ResponseEntity, error) {
	var (
		ch = make(chan PubSubChan[string, []byte])

		entity ResponseEntity

		reqId             = uuid.NewV4().String()
		subCtx, subCancel = context.WithTimeout(ctx, c.timeout)
	)
	defer subCancel()

	c.pubSubReq.Subscribe(subCtx, reqId, ch)

	if tries < 1 {
		tries = 1
	}
	c.queue(&clientRequest{
		tryLeft: tries,
		body: clientEntity{
			ClientId:  c.clientId,
			Version:   c.version,
			RequestId: reqId,
			Command:   command,
			Payload:   data,
		},
	})

	select {
	case <-subCtx.Done():
		select {
		case <-ctx.Done():
			return nil, ErrCanceled
		default:
			return nil, ErrTimeout
		}
	case dataResp := <-ch:
		if err := json.Unmarshal(dataResp.Data, &entity); err != nil {
			return nil, errors.Join(err, ErrJsonParse)
		}
		return &entity, nil
	}
}

func (c *Client[T]) SendAsync(command string, data any) {
	c.SendTriesAsync(command, 1, data)
}

func (c *Client[T]) SendTriesAsync(command string, tries int, data any) {
	c.sendTriesAsync(command, tries, data)
}

func (c *Client[T]) sendTriesAsync(command string, tries int, data any) {
	if tries < 1 {
		tries = 1
	}
	c.queue(&clientRequest{
		tryLeft: tries,
		body: clientEntity{
			ClientId:  c.clientId,
			Version:   c.version,
			RequestId: uuid.NewV4().String(),
			Command:   command,
			Payload:   data,
		},
	})
}

func (c *Client[T]) OnDialErr(f func(*Client[T])) {
	c.onDialError = f
}
func (c *Client[T]) OnConnected(f func(*Client[T])) {
	c.onConnected = f
}
func (c *Client[T]) OnClosed(f func(*Client[T])) {
	c.onClosed = f
}

func (c *Client[T]) loop(ctx context.Context, conn *websocket.Conn) {
	var (
		err  error
		data []byte

		wg sync.WaitGroup

		subCtx, subCancel = context.WithCancel(ctx)
	)

	//建立监听外部退出任务 & 心跳包发送
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(c.timeout)
		defer timer.Stop()
		defer conn.Close() //处理外部退出时关闭conn
		for {
			select {
			case <-subCtx.Done():
				return
			case <-timer.C:
			}
			c.Send(subCtx, "ping", nil) //主动发心跳包
			timer.Reset(c.timeout)
		}
	}()

	//建立发送任务线程
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.runSending(subCtx, conn)
	}()

	//建立读取任务线程
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer subCancel()
		for {
			var resp struct {
				RequestId string          `json:"request_id"`
				Type      string          `json:"type"` //response push
				Body      json.RawMessage `json:"body"`
			}
			if _, data, err = conn.ReadMessage(); err != nil {
				return
			}
			if err = json.Unmarshal(data, &resp); err != nil {
				return
			}
			if resp.Type == "push" {
				var tResp struct {
					Command T `json:"command"`
				}
				if err = json.Unmarshal(data, &tResp); err != nil {
					return
				}
				c.pubSub.Publish(tResp.Command, resp.Body)
				continue
			}

			if val, ok := c.querying.LoadAndDelete(resp.RequestId); ok {
				ent := val.(*clientRequest)
				if ent.cancelWaiting != nil {
					ent.cancelWaiting() //快速释放等待超时的线程
				}
				//发布响应数据通知
				c.pubSubReq.Publish(resp.RequestId, resp.Body)
			}
		}
	}()

	//connected事件
	if callback := c.onConnected; callback != nil {
		callback(c)
	}

	//把正在发送中的队列重发
	histories := make([]*clientRequest, 0)
	c.querying.Range(func(key, value any) bool {
		histories = append(histories, value.(*clientRequest))
		c.querying.Delete(key)
		return true
	})
	c.queue(histories...)

	wg.Wait()

	//closed事件
	if callback := c.onClosed; callback != nil {
		callback(c)
	}
}

func (c *Client[T]) runSending(ctx context.Context, conn *websocket.Conn) {
	var (
		err error
		wg  sync.WaitGroup
	)
	defer wg.Wait()

	//接收请求向ws写入
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.notify:
		}

		for _, req := range c.dequeueAll() {
			//没有重试机会的立即失败回调
			if req.tryLeft <= 0 {
				c.publishAttemptsExhausted(req.body.RequestId)
				continue
			}

			//保存到发送中队列
			subCtx, subCancel := context.WithCancel(context.Background())
			req.cancelWaiting = subCancel
			c.querying.Store(req.body.RequestId, req)

			//发送
			if err = conn.WriteJSON(req.body); err == nil {
				req.tryLeft-- //只有真正写入conn，才扣除重试次数
			}

			//设定定时器，超时继续重试
			wg.Add(1)
			go func(requestId string) {
				defer wg.Done()
				timer := time.NewTimer(c.timeout)
				defer timer.Stop()

				select {
				case <-ctx.Done(): //外部退出执行这里
					return
				case <-subCtx.Done(): //成功收到发送响应，会执行这里，为了快速释放等待资源
					return
				case <-timer.C:
				}
				//超时执行重试
				if val, ok := c.querying.LoadAndDelete(requestId); ok {
					c.queue(val.(*clientRequest))
				}
			}(req.body.RequestId)
		}
	}
}

func (c *Client[T]) publishAttemptsExhausted(requestId string) {
	data, _ := json.Marshal(struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}{
		Code:    1,
		Message: "attempts exhausted",
		Data:    nil,
	})
	c.pubSubReq.Publish(requestId, data)
}

func (c *Client[T]) queue(items ...*clientRequest) {
	c.queueMux.Lock()
	defer c.queueMux.Unlock()

	for _, item := range items {
		c.queueItems = append(c.queueItems, item)
	}
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *Client[T]) dequeueAll() []*clientRequest {
	c.queueMux.Lock()
	defer c.queueMux.Unlock()

	arr := c.queueItems
	c.queueItems = make([]*clientRequest, 0)
	return arr
}

func (c *Client[T]) Subscribe(ctx context.Context, topic T, ch chan<- PubSubChan[T, []byte]) {
	c.pubSub.Subscribe(ctx, topic, ch)
}
