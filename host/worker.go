package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type Node struct {
	Name        string `json:"name"`
	Total       uint64 `json:"total"`
	Free        uint64 `json:"free"`
	Used        uint64 `json:"used"`
	Allocation  uint64 `json:"allocation"`
	Cores       int    `json:"cores"`
	Build       string `json:"build"`
	Address     string `json:"address"`
	pin, secret string
}

func parseCode(s string) (Node, error) {
	p := strings.Split(strings.TrimSpace(s), "|")
	bad := errors.New("Niepoprawny kod sesji YourLM")
	if len(p) != 4 || p[0] != "YLM1" {
		return Node{}, bad
	}
	h, port, e := net.SplitHostPort(p[1])
	ip := net.ParseIP(h)
	n, e2 := strconv.Atoi(port)
	if e != nil || e2 != nil || n < 1 || n > 65535 || ip.To4() == nil || !ip.IsPrivate() {
		return Node{}, bad
	}
	for i, size := range []int{32, 24} {
		v, e := hex.DecodeString(p[i+2])
		if e != nil || len(v) != size {
			return Node{}, bad
		}
	}
	return Node{Address: p[1], pin: p[2], secret: p[3]}, nil
}
func tuneTCP(conn net.Conn) {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		conn = tlsConn.NetConn()
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetReadBuffer(1 << 20)
		_ = tcp.SetWriteBuffer(1 << 20)
	}
}

func (n Node) dial(mode string) (net.Conn, error) {
	pin, _ := hex.DecodeString(n.pin)
	c, e := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp4", n.Address, &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, // Trust the certificate fingerprint transferred out of band, not a public CA.
		VerifyConnection: func(s tls.ConnectionState) error {
			if len(s.PeerCertificates) > 0 {
				v := sha256.Sum256(s.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(v[:], pin) == 1 {
					return nil
				}
			}
			return errors.New("Certyfikat workera nie pasuje do kodu QR")
		}})
	if e != nil {
		return nil, e
	}
	tuneTCP(c)
	c.SetDeadline(time.Now().Add(6 * time.Second))
	_, e = fmt.Fprintf(c, "%s %s\n", mode, n.secret)
	if e != nil {
		c.Close()
		return nil, e
	}
	return c, nil
}

const GiB uint64 = 1 << 30

func (n *Node) refresh() error {
	c, e := n.dial("INFO")
	if e != nil {
		return e
	}
	defer c.Close()
	var info Node
	e = json.NewDecoder(io.LimitReader(c, 4096)).Decode(&info)
	if e != nil {
		return e
	}
	if info.Build != "b12562" || info.Total == 0 || info.Free > info.Total || info.Cores <= 0 {
		return errors.New("Niekompatybilny worker")
	}
	if info.Used == 0 || info.Used > info.Total {
		info.Used = info.Total - info.Free
	}
	if info.Allocation > info.Total {
		info.Allocation = 0
	}
	n.Name, n.Free, n.Total, n.Used, n.Allocation, n.Cores, n.Build =
		info.Name, info.Free, info.Total, info.Used, info.Allocation, info.Cores, info.Build
	return nil
}
func copyRPC(dst io.Writer, src io.Reader) {
	_, _ = io.CopyBuffer(dst, src, make([]byte, 256<<10))
}

func (n Node) bridge() (net.Listener, error) {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	go func() {
		for {
			local, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer local.Close()
				tuneTCP(local)
				remote, e := n.dial("RPC")
				if e != nil {
					return
				}
				defer remote.Close()
				r := bufio.NewReader(remote)
				b, e := r.ReadByte()
				if e != nil || b != '!' {
					return
				}
				remote.SetDeadline(time.Time{})
				go func() { copyRPC(remote, local); remote.Close() }()
				copyRPC(local, r)
			}()
		}
	}()
	return l, nil
}
