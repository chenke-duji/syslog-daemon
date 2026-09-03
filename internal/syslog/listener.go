package syslog

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// HandleFunc processes a parsed syslog message. It must be fast (the listener
// runs in parallel workers); heavy work should be enqueued, not done inline.
type HandleFunc func(msg *Message, sourceIP string)

// Listener receives syslog messages over UDP and dispatches them to a pool of
// worker goroutines for high throughput.
type Listener struct {
	addr        string
	readBuffer  int          // SO_RCVBUF size in bytes (0 = kernel default)
	workers     int          // number of processing workers
	maxMsgSize  int          // max syslog message size
	allowedNets []*net.IPNet // optional source IP/CIDR allowlist; empty = accept all
	handle      HandleFunc
	log         *slog.Logger

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
	ListenAddr   string   `yaml:"listenAddr"`
	Workers      int      `yaml:"workers"`
	ReadBuffer   int      `yaml:"readBufferBytes"`
	MaxMessage   int      `yaml:"maxMessageBytes"`
	AllowedCIDRs []string `yaml:"allowedCIDRs"`
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

	// Parse CIDR allowlist.
	var allowedNets []*net.IPNet
	if len(cfg.AllowedCIDRs) > 0 {
		allowedNets = make([]*net.IPNet, 0, len(cfg.AllowedCIDRs))
		for _, cidr := range cfg.AllowedCIDRs {
			// If no /prefix, treat as /32 (IPv4) or /128 (IPv6).
			if !strings.Contains(cidr, "/") {
				ip := net.ParseIP(cidr)
				if ip == nil {
					return nil, fmt.Errorf("syslog: invalid allowedCIDRs entry %q", cidr)
				}
				if ip.To4() != nil {
					cidr += "/32"
				} else {
					cidr += "/128"
				}
			}
			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return nil, fmt.Errorf("syslog: invalid allowedCIDRs entry %q: %w", cidr, err)
			}
			allowedNets = append(allowedNets, ipNet)
		}
		log.Info("syslog source IP allowlist configured", "entries", len(allowedNets))
	} else {
		log.Warn("no allowedCIDRs configured; all source IPs will be accepted (insecure)")
	}

	return &Listener{
		addr:        cfg.ListenAddr,
		readBuffer:  cfg.ReadBuffer,
		workers:     cfg.Workers,
		maxMsgSize:  cfg.MaxMessage,
		allowedNets: allowedNets,
		handle:      handle,
		log:         log,
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
	var next atomic.Uint64
	for i := 0; i < l.workers; i++ {
		ch := make(chan *packet, 1024)
		l.handlers[i] = ch
		l.wg.Add(1)
		go l.worker(ctx, ch)
	}

	// Reader goroutine(s): pull datagrams and round-robin to workers.
	reader := func() {
		defer l.wg.Done()
		buf := make([]byte, l.maxMsgSize)
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
			idx := int(next.Add(1) % uint64(l.workers))
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
	// Source IP allowlist check.
	if len(l.allowedNets) > 0 && pkt.addr != nil {
		ip := pkt.addr.IP
		allowed := false
		for _, n := range l.allowedNets {
			if n.Contains(ip) {
				allowed = true
				break
			}
		}
		if !allowed {
			l.log.Warn("syslog: packet from non-allowlisted source dropped",
				"sourceIp", ip.String())
			return
		}
	}

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
