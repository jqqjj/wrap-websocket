package wrap

type handle struct {
	handler     HandleFunc
	middlewares []Middleware
}

type HandleFunc func(r *Request, w *Response)

type Middleware func(next HandleFunc, r *Request, w *Response)
