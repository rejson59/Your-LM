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
	listener, pairListener                 net.Listener
	cmd                                    *exec.Cmd
	code, secret, local, manualKey, pairAt string
	slots                                  chan struct{}
}

func removeStaleRPCParts(root string) {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasPrefix(entry.Name(), ".incoming-") && strings.HasSuffix(entry.Name(), ".part") {
			_ = os.Remove(path)
		}
		return nil
	})
}

func (o *Offer) close() {
	if o.listener != nil {
		o.listener.Close()
	}
	if o.pairListener != nil {
		o.pairListener.Close()
	}
	if o.cmd != nil && o.cmd.Process != nil {
		o.cmd.Process.Kill()
	}
}
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
	pairListener, e := net.Listen("tcp4", ip+":0")
	if e != nil {
		l.Close()
		return e
	}
	local, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		l.Close()
		pairListener.Close()
		return e
	}
	addr := local.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	local.Close()
	cache := filepath.Join(a.dir, "rpc-cache")
	if e := os.MkdirAll(cache, 0700); e != nil {
		l.Close()
		pairListener.Close()
		return e
	}
	removeStaleRPCParts(cache)
	cmd := exec.Command(path, "-H", "127.0.0.1", "-p", port, "-t", strconv.Itoa(max(1, runtime.NumCPU()/2)), "-c")
	cmd.Dir = filepath.Dir(path)
	cmd.Env = append(childEnv(), "LLAMA_CACHE="+cache)
	prepareChild(cmd)
	if e = cmd.Start(); e != nil {
		l.Close()
		pairListener.Close()
		return e
	}
	trackChild(cmd)
	manual := manualCode()
	canonical, _ := canonicalManual(manual)
	pin := fmt.Sprintf("%x", sha256.Sum256(der))
	o := &Offer{
		listener:     l,
		pairListener: pairListener,
		cmd:          cmd,
		secret:       derivedSessionSecret(canonical),
		local:        addr,
		manualKey:    manual,
		pairAt:       pairListener.Addr().String(),
		slots:        make(chan struct{}, 16),
	}
	o.code = fmt.Sprintf("YLM1|%s|%s|%s", l.Addr(), pin, o.secret)
	a.off = o
	go func() {
		cmd.Wait()
		l.Close()
		pairListener.Close()
		a.Lock()
		if a.off == o {
			a.off = nil
		}
		a.Unlock()
	}()
	go func() {
		for {
			c, err := pairListener.Accept()
			if err != nil {
				return
			}
			serveManualPair(c, canonical, l.Addr().String(), pin)
		}
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
	tuneTCP(c)
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
		stats := systemMemory()
		allocation := uint64(0)
		if o.cmd != nil && o.cmd.Process != nil {
			allocation = memoryForProcess(o.cmd.Process.Pid).Resident
		}
		json.NewEncoder(c).Encode(Node{
			Name: n, Total: stats.Total, Free: stats.Available, Used: stats.Used,
			Allocation: allocation, Cores: runtime.NumCPU(), Build: rpcWorkerBuild,
		})
	case "DIAG":
		n, _ := os.Hostname()
		stats := systemMemory()
		allocation := uint64(0)
		alive := false
		if o.cmd != nil && o.cmd.Process != nil {
			allocation = memoryForProcess(o.cmd.Process.Pid).Resident
			alive = o.cmd.ProcessState == nil || !o.cmd.ProcessState.Exited()
		}
		json.NewEncoder(c).Encode(map[string]any{
			"device":               n,
			"os":                   runtime.GOOS,
			"workerBuild":          rpcWorkerBuild,
			"rpcAlive":             alive,
			"rpcResidentBytes":     allocation,
			"memoryTotalBytes":     stats.Total,
			"memoryAvailableBytes": stats.Available,
		})
	case "RPC":
		local, e := net.DialTimeout("tcp4", o.local, time.Second)
		if e != nil {
			return
		}
		defer local.Close()
		tuneTCP(local)
		c.Write([]byte{'!'})
		c.SetDeadline(time.Time{})
		go func() { copyRPC(c, local, nil); c.Close() }()
		copyRPC(local, r, nil)
	}
}
