package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Offer a desktop CPU through the same pinned-TLS protocol as Android.
type Offer struct {
	listener            net.Listener
	cmd                 *exec.Cmd
	code, secret, local string
	slots               chan struct{}
}

func (o *Offer) close() { o.listener.Close(); o.cmd.Process.Kill() }
func (a *App) offer(ip string) error {
	if a.s != nil || a.off != nil {
		return errors.New("Zatrzymaj aktywną sesję")
	}
	valid := false
	for _, s := range ips() {
		if s == ip && net.ParseIP(s).IsPrivate() {
			valid = true
		}
	}
	if !valid {
		return errors.New("Wybierz prywatny adres IPv4 sieci LAN")
	}
	path, e := a.launchPath(true)
	if e != nil {
		return e
	}
	if _, e := os.Stat(path); e != nil {
		return fmt.Errorf("Brak ggml-rpc-server.exe obok silnika: %w", e)
	}
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "YourLM"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &k.PublicKey, k)
	if e != nil {
		return e
	}
	l, e := tls.Listen("tcp4", ip+":0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: k}}, MinVersion: tls.VersionTLS12})
	if e != nil {
		return e
	}
	local, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		l.Close()
		return e
	}
	addr := local.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	local.Close()
	cmd := exec.Command(path, "-H", "127.0.0.1", "-p", port, "-t", strconv.Itoa(max(1, runtime.NumCPU()/2)))
	cmd.Dir = filepath.Dir(path)
	cmd.Env = childEnv()
	prepareChild(cmd)
	if e = cmd.Start(); e != nil {
		l.Close()
		return e
	}
	trackChild(cmd)
	o := &Offer{listener: l, cmd: cmd, secret: token(), local: addr, slots: make(chan struct{}, 16)}
	o.code = fmt.Sprintf("YLM1|%s|%x|%s", l.Addr(), sha256.Sum256(der), o.secret)
	a.off = o
	go func() {
		cmd.Wait()
		l.Close()
		a.Lock()
		if a.off == o {
			a.off = nil
		}
		a.Unlock()
	}()
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			select {
			case o.slots <- struct{}{}:
				go o.serve(c)
			default:
				c.Close()
			}
		}
	}()
	return nil
}
func (o *Offer) serve(c net.Conn) {
	defer func() { c.Close(); <-o.slots }()
	c.SetDeadline(time.Now().Add(6 * time.Second))
	r := bufio.NewReaderSize(c, 128)
	line, e := r.ReadSlice('\n')
	if e != nil {
		return
	}
	mode, key, ok := strings.Cut(strings.TrimSpace(string(line)), " ")
	if !ok || subtle.ConstantTimeCompare([]byte(key), []byte(o.secret)) != 1 {
		return
	}
	switch mode {
	case "INFO":
		n, _ := os.Hostname()
		total, free := memory()
		json.NewEncoder(c).Encode(Node{Name: n, Total: total, Free: free, Cores: runtime.NumCPU(), Build: "b12562"})
	case "RPC":
		local, e := net.DialTimeout("tcp4", o.local, time.Second)
		if e != nil {
			return
		}
		defer local.Close()
		c.Write([]byte{'!'})
		c.SetDeadline(time.Time{})
		go func() { io.Copy(c, local); c.Close() }()
		io.Copy(local, r)
	}
}
