package syslog

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"
)

// HandleFunc processes a parsed syslog message. It must be fast (the listener
// runs in parallel workers); heavy work should be enqueued, not done inline.
type HandleFunc func(msg *Message, sourceIP string)

// Listener receives syslog messages over UDP and dispatches them to a pool of
// worker goroutines for high throughput.
type Listener struct {
	addr       string
	readBuffer int          // SO_RCVBUF size in bytes (0 = kernel default)
	workers    int          // number of processing workers
	batchMsgs  int          // max syslog message size
	handle     HandleFunc
	log        *slog.Logger

	conn     *net.UDPConn
	wg       sync.WaitGroup
	handlers []chan *packet
	cancel   context.CancelFunc
}

// packet bundles a datagram with its source address.
type packet struct {
	data []byte
	addr *net.UDPAddr
}

// Config holds listener tuning parameters.
type Config struct {
	ListenAddr string `yaml:"listenAddr"`
	Workers    int    `yaml:"workers"`
	ReadBuffer int    `yaml:"readBufferBytes"`
	MaxMessage int    `yaml:"maxMessageBytes"`
}

// DefaultConfig returns sensible listener defaults.
func DefaultConfig() Config {
	return Config{
		ListenAddr: "0.0.0.0:514",
		Workers:    8,
		ReadBuffer: 4 << 20, // 4 MiB
		MaxMessage: 65536,
	}
}

// NewListener builds a UDP syslog listener.
func NewListener(cfg Config, handle HandleFunc, log *slog.Logger) (*Listener, error) {
	if handle == nil {
		handle = func(*Message, string) {}
	}
	if cfg.Workers <= 0 {
		cfg.Workers = DefaultConfig().Workers
	}
	if cfg.MaxMessage <= 0 {
		cfg.MaxMessage = DefaultConfig().MaxMessage
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultConfig().ListenAddr
	}
	if log == nil {
		log = slog.Default()
	}
	return &Listener{
		addr:       cfg.ListenAddr,
		readBuffer: cfg.ReadBuffer,
		workers:    cfg.Workers,
		batchMsgs:  cfg.MaxMessage,
		handle:     handle,
		log:        log,
	}, nil
}

// Start binds the UDP socket and launches the read loop and worker pool.
func (l *Listener) Start() error {
	udpAddr, err := net.ResolveUDPAddr("udp", l.addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	if l.readBuffer > 0 {
		_ = conn.SetReadBuffer(l.readBuffer)
	}
	l.conn = conn

	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel

	// Worker pool: each worker owns a buffered channel to avoid lock contention.
	l.handlers = make([]chan *packet, l.workers)
	next := 0
	var mutex sync.Mutex
	for i := 0; i < l.workers; i++ {
		ch := make(chan *packet, 1024)
		l.handlers[i] = ch
		l.wg.Add(1)
		go l.worker(ctx, ch)
	}

	// Reader goroutine(s): pull datagrams and round-robin to workers.
	reader := func() {
		defer l.wg.Done()
		buf := make([]byte, l.batchMsgs)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					// Transient error: brief backoff then continue.
					time.Sleep(10 * time.Millisecond)
					continue
				}
			}
			if n == 0 {
				continue
			}
			data := make([]byte, n)
			copy(data, buf[:n])
			pkt := &packet{data: data, addr: addr}
			mutex.Lock()
			idx := next
			next = (next + 1) % l.workers
			mutex.Unlock()
			select {
			case l.handlers[idx] <- pkt:
			case <-ctx.Done():
				return
			}
		}
	}
	l.wg.Add(1)
	go reader()

	l.log.Info("syslog listener started", "addr", l.addr, "workers", l.workers)
	return nil
}

// worker parses datagrams and calls the handle function.
func (l *Listener) worker(ctx context.Context, ch <-chan *packet) {
	defer l.wg.Done()
	for {
		select {
		case <-ctx.Done():
			// Drain remaining packets best-effort.
			for {
				select {
				case pkt := <-ch:
					l.process(pkt)
				default:
					return
				}
			}
		case pkt := <-ch:
			l.process(pkt)
		}
	}
}

func (l *Listener) process(pkt *packet) {
	msg, err := Parse(string(pkt.data))
	if err != nil {
		// Still forward lenient-parsed message if it has content.
		if msg == nil || msg.Message == "" {
			l.log.Debug("syslog: unparseable message dropped",
				"sourceIp", pkt.addr.IP.String(), "err", err)
			return
		}
	}
	l.handle(msg, pkt.addr.IP.String())
}

// Addr returns the bound local address.
func (l *Listener) Addr() net.Addr {
	if l.conn == nil {
		return nil
	}
	return l.conn.LocalAddr()
}

// Close stops the listener and waits for workers to finish.
func (l *Listener) Close() {
	if l.cancel != nil {
		l.cancel()
	}
	if l.conn != nil {
		_ = l.conn.Close()
	}
	l.wg.Wait()
}
