package wrap

type Push struct {
	conn *Conn
}

func NewPush(conn *Conn) *Push {
	return &Push{
		conn: conn,
	}
}

func (p *Push) GetConn() *Conn {
	return p.conn
}

func (p *Push) Send(command string, object any) {
	resp := struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Body    any    `json:"body"`
	}{
		Type:    "push",
		Command: command,
		Body:    object,
	}

	p.conn.sendEntity(resp)
}
