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

type Client struct {
	clientId string
	addr     string
	version  string
	timeout  time.Duration

	notify     chan struct{}
	queueMux   sync.Mutex
	queueItems []clientRequest
	querying   sync.Map
	pubSub     *PubSub[string, []byte]

	onDialError func(*Client)
	onConnected func(*Client)
	onClosed    func(*Client)
}

func NewClient(clientId, addr, version string, timeoutDuration time.Duration) *Client {
	if timeoutDuration < time.Second {
		timeoutDuration = time.Second * 30
	}
	return &Client{
		clientId:   clientId,
		addr:       addr,
		version:    version,
		timeout:    timeoutDuration,
		notify:     make(chan struct{}, 1),
		queueItems: make([]clientRequest, 0),
		pubSub:     NewPubSub[string, []byte](),
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

		//connected事件
		if callback := c.onConnected; callback != nil {
			callback(c)
		}

		c.loop(ctx, conn)

		//closed事件
		if callback := c.onClosed; callback != nil {
			callback(c)
		}

		timer.Reset(time.Millisecond)
	}
}

func (c *Client) Send(ctx context.Context, command string, data any) (*ResponseEntity, error) {
	return c.SendTries(ctx, 1, command, data)
}

func (c *Client) SendTries(ctx context.Context, tries int, command string, data any) (*ResponseEntity, error) {
	var (
		ch = make(chan []byte)

		entity ResponseEntity

		reqId             = uuid.NewV4().String()
		subCtx, subCancel = context.WithTimeout(ctx, c.timeout)
	)
	defer subCancel()

	c.pubSub.Subscribe(subCtx, reqId, ch)

	if tries < 1 {
		tries = 1
	}
	c.queue(clientRequest{
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
		if err := json.Unmarshal(dataResp, &entity); err != nil {
			return nil, errors.Join(err, ErrJsonParse)
		}
		return &entity, nil
	}
}

func (c *Client) SendAsync(command string, data any) {
	c.SendTriesAsync(command, 1, data)
}

func (c *Client) SendTriesAsync(command string, tries int, data any) {
	var (
		reqId = uuid.NewV4().String()
	)
	if tries < 1 {
		tries = 1
	}
	c.queue(clientRequest{
		tryLeft: tries,
		body: clientEntity{
			ClientId:  c.clientId,
			Version:   c.version,
			RequestId: reqId,
			Command:   command,
			Payload:   data,
		},
	})
}

func (c *Client) OnDialErr(f func(*Client)) {
	c.onDialError = f
}
func (c *Client) OnConnected(f func(*Client)) {
	c.onConnected = f
}
func (c *Client) OnClosed(f func(*Client)) {
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
	c.queue(histories...)

	for {
		var resp struct {
			RequestId string          `json:"request_id"`
			Type      string          `json:"type"` //response push
			Command   string          `json:"command"`
			Body      json.RawMessage `json:"body"`
		}
		if _, data, err = conn.ReadMessage(); err != nil {
			return
		}
		if err = json.Unmarshal(data, &resp); err != nil {
			return
		}
		if resp.Type == "push" {
			c.pubSub.Publish("#"+resp.Command+"#", resp.Body)
			continue
		}

		if val, ok := c.querying.LoadAndDelete(resp.RequestId); ok {
			ent := val.(clientRequest)
			if ent.cancelWaiting != nil {
				ent.cancelWaiting() //快速释放等待超时的线程
			}
			//发布响应数据通知
			c.pubSub.Publish(resp.RequestId, resp.Body)
		}
	}
}

func (c *Client) runSending(ctx context.Context, conn *websocket.Conn) {
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
				data, _ := json.Marshal(struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
					Data    any    `json:"data"`
				}{
					Code:    1,
					Message: "attempts exhausted",
					Data:    nil,
				})
				c.pubSub.Publish(req.body.RequestId, data)
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
				ticker := time.NewTimer(c.timeout)
				defer ticker.Stop()
				select {
				case <-ctx.Done(): //外部退出执行这里
					return
				case <-subCtx.Done(): //成功收到发送响应，会执行这里，为了快速释放等待资源
					return
				case <-ticker.C:
				}
				//超时执行重试
				if val, ok := c.querying.LoadAndDelete(requestId); ok {
					c.queue(val.(clientRequest))
				}
			}(req.body.RequestId)
		}
	}
}

func (c *Client) queue(items ...clientRequest) {
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

func (c *Client) dequeueAll() []clientRequest {
	c.queueMux.Lock()
	defer c.queueMux.Unlock()

	arr := c.queueItems
	c.queueItems = make([]clientRequest, 0)
	return arr
}

func (c *Client) Subscribe(ctx context.Context, topic string, ch chan<- []byte) {
	c.pubSub.Subscribe(ctx, "#"+topic+"#", ch)
}
