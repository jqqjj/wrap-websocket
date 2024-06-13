package wrap

type GroupServer struct {
	commandPrefix string
	parent        *GroupServer
	handles       map[string]handle
	middlewares   []Middleware
}

func NewGroup() *GroupServer {
	return &GroupServer{
		handles: make(map[string]handle),
	}
}

func (s *GroupServer) SetHandle(command string, handleFunc HandleFunc, middlewares ...Middleware) {
	root := s.getRoot()

	fullCommand := s.buildPrefix() + command

	if _, ok := root.handles[fullCommand]; ok {
		panic("duplicate command")
	}
	root.handles[fullCommand] = handle{
		handler:     handleFunc,
		middlewares: append(s.middlewares, middlewares...),
	}
}

func (s *GroupServer) Use(middlewares ...Middleware) {
	s.middlewares = append(s.middlewares, middlewares...)
}

func (s *GroupServer) Group(commandPrefix string, middlewares ...Middleware) *GroupServer {
	groupServer := NewGroup()
	groupServer.parent = s
	groupServer.commandPrefix = commandPrefix
	groupServer.middlewares = append(s.middlewares, middlewares...)
	return groupServer
}

func (s *GroupServer) getRoot() *GroupServer {
	root := s
	for root.parent != nil {
		root = root.parent
	}
	return root
}

func (s *GroupServer) buildPrefix() string {
	root := s
	prefix := root.commandPrefix
	for root.parent != nil {
		root = root.parent
		prefix = root.commandPrefix + prefix
	}
	return prefix
}
