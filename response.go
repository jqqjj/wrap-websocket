package wrap

type ResponseBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

type Response struct {
	filled bool
	body   ResponseBody
	conn   *Conn

	closed bool
}

func NewResponse(conn *Conn) *Response {
	return &Response{
		conn: conn,
	}
}

func (r *Response) GetConn() *Conn {
	return r.conn
}

func (r *Response) Close() {
	r.closed = true
}

func (r *Response) GetResponseBody() ResponseBody {
	return r.body
}

func (r *Response) SetResponseBody(body ResponseBody) {
	r.filled = true
	r.body = body
}

func (r *Response) Success(object any) {
	r.fill(0, "Success", object)
}

func (r *Response) Fail() {
	r.fill(1, "Fail", nil)
}

func (r *Response) FailWithCode(code int) {
	r.fill(code, "Fail", nil)
}

func (r *Response) FailWithMessage(message string) {
	r.fill(1, message, nil)
}

func (r *Response) FailWithCodeAndMessage(code int, message string) {
	r.fill(code, message, nil)
}

func (r *Response) fill(code int, message string, object any) {
	if r.filled {
		return
	}
	r.filled = true

	r.body = ResponseBody{
		Code:    code,
		Message: message,
		Data:    object,
	}
}
