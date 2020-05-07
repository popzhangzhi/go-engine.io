package engineio

import (
	"fmt"
	"github.com/googollee/go-engine.io/base"
	"github.com/googollee/go-engine.io/transport"
	"github.com/googollee/go-engine.io/transport/polling"
	"github.com/googollee/go-engine.io/transport/websocket"
	websocket2 "github.com/gorilla/websocket"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

func defaultChecker(*http.Request) (http.Header, error) {
	return nil, nil
}

func defaultInitor(*http.Request, Conn) {}

// Options is options to create a server.
type Options struct {
	RequestChecker     func(*http.Request) (http.Header, error)
	ConnInitor         func(*http.Request, Conn)
	PingTimeout        time.Duration
	PingInterval       time.Duration
	Transports         []transport.Transport
	SessionIDGenerator SessionIDGenerator
	ChanNumber         uint
}

func (c *Options) getRequestChecker() func(*http.Request) (http.Header, error) {
	if c != nil && c.RequestChecker != nil {
		return c.RequestChecker
	}
	return defaultChecker
}

func (c *Options) getConnInitor() func(*http.Request, Conn) {
	if c != nil && c.ConnInitor != nil {
		return c.ConnInitor
	}
	return defaultInitor
}

func (c *Options) getPingTimeout() time.Duration {
	if c != nil && c.PingTimeout != 0 {
		return c.PingTimeout
	}
	return time.Minute
}

func (c *Options) getPingInterval() time.Duration {
	if c != nil && c.PingInterval != 0 {
		return c.PingInterval
	}
	return time.Second * 20
}

func (c *Options) getTransport() []transport.Transport {
	if c != nil && len(c.Transports) != 0 {
		return c.Transports
	}
	return []transport.Transport{
		polling.Default,
		websocket.Default,
	}
}

func (c *Options) getSessionIDGenerator() SessionIDGenerator {
	if c != nil && c.SessionIDGenerator != nil {
		return c.SessionIDGenerator
	}
	return &defaultIDGenerator{}
}
func (c *Options) getChanNumber() uint {
	if c != nil && c.ChanNumber > 0 {
		return c.ChanNumber
	}
	return 1

}

// Server is server.
type Server struct {
	transports     *transport.Manager
	pingInterval   time.Duration
	pingTimeout    time.Duration
	sessions       *manager
	requestChecker func(*http.Request) (http.Header, error)
	connInitor     func(*http.Request, Conn)
	connChan       chan Conn
	closeOnce      sync.Once
}

// NewServer returns a server.
func NewServer(opts *Options) (*Server, error) {
	t := transport.NewManager(opts.getTransport())
	return &Server{
		transports:     t,
		pingInterval:   opts.getPingInterval(),
		pingTimeout:    opts.getPingTimeout(),
		requestChecker: opts.getRequestChecker(),
		connInitor:     opts.getConnInitor(),
		sessions:       newManager(opts.getSessionIDGenerator()),
		connChan:       make(chan Conn, opts.getChanNumber()),
	}, nil
}

// Close closes server.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.connChan)
	})
	return nil
}

// Accept accepts a connection.
func (s *Server) Accept() (Conn, error) {
	c := <-s.connChan
	if c == nil {
		return nil, io.EOF
	}
	return c, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	sid := query.Get("sid")
	session := s.sessions.Get(sid)
	t := query.Get("transport")
	tspt := s.transports.Get(t)
	fmt.Println("server.go:", "sid", sid, "session", session != nil, "transport", t)
	if tspt == nil {
		http.Error(w, "invalid transport", http.StatusBadRequest)
		return
	}
	header, err := s.requestChecker(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for k, v := range header {
		w.Header()[k] = v
	}

	//在每个用户第一次连接时，没有session去创建session，协程写入session。之前Serve那协程读取
	if session == nil {
		if sid != "" {
			fmt.Println("invalid sid")
			http.Error(w, "invalid sid", http.StatusBadRequest)
			return
		}
		//调用对应协议的类生成conn
		conn, err := tspt.Accept(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		params := base.ConnParameters{
			PingInterval: s.pingInterval,
			PingTimeout:  s.pingTimeout,
			Upgrades:     s.transports.UpgradeFrom(t),
		}
		session, err = newSession(s.sessions, t, conn, params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		s.connInitor(r, session)
		//获取下个io.write把相关参数吸入encoder。最后把整个session写入channel
		go func() {
			w, err := session.nextWriter(base.FrameString, base.OPEN)
			if err != nil {
				session.Close()
				return
			}
			if _, err := session.params.WriteTo(w); err != nil {
				w.Close()
				session.Close()
				return
			}
			if err := w.Close(); err != nil {
				session.Close()
				return
			}
			s.connChan <- session
		}()
	}

	//这个开始判断协议和session不一致后进行调用新协议（当前只可能是websocket）的accept
	if session.Transport() != t {
		conn, err := tspt.Accept(w, r)
		if err != nil {
			log.Println("server.go:190", err.Error())
			// don't call http.Error() for HandshakeErrors because
			// they get handled by the websocket library internally.
			if _, ok := err.(websocket2.HandshakeError); !ok {
				http.Error(w, err.Error(), http.StatusBadGateway)
			}
			return
		}
		session.upgrade(t, conn)
		if handler, ok := conn.(http.Handler); ok {
			handler.ServeHTTP(w, r)
		}
		return
	}
	session.serveHTTP(w, r)
}

func (s *Server) GetAllSessions() (int, []string) {
	count, names := s.sessions.GetAllSessions()
	return count, names
}
