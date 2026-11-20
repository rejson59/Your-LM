package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const manualAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var manualCodePattern = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{20}$`)

// manualCode is deliberately short enough to type but still carries 100 bits
// of entropy. It is never sent over the network. The host proves knowledge of
// it with HMAC and pins the worker certificate before opening TLS.
func manualCode() string {
	b := make([]byte, 20)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = manualAlphabet[int(b[i])&31]
	}
	return groupManual(string(b))
}

func canonicalManual(s string) (string, error) {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "", "\n", "", "\r", "").Replace(s))
	if !manualCodePattern.MatchString(s) {
		return "", errors.New("Kod ma 20 znaków w czterech grupach")
	}
	return s, nil
}

func groupManual(s string) string {
	if len(s) != 20 {
		return s
	}
	return s[:5] + "-" + s[5:10] + "-" + s[10:15] + "-" + s[15:]
}

func derivedSessionSecret(key string) string {
	sum := sha256.Sum256([]byte("YourLM-auth-v2|" + key))
	return hex.EncodeToString(sum[:24])
}

func pairingProof(key, nonce, address, pin string) string {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "YLM2|%s|%s|%s", nonce, address, pin)
	return hex.EncodeToString(mac.Sum(nil))
}

func privateEndpoint(s string, loopback bool) (string, string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(s))
	ip := net.ParseIP(host)
	if err != nil || ip == nil || ip.To4() == nil || (!ip.IsPrivate() && !(loopback && ip.IsLoopback())) {
		return "", "", errors.New("Podaj prywatny adres IPv4 i port pokazany na workerze")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", "", errors.New("Podaj prywatny adres IPv4 i port pokazany na workerze")
	}
	return ip.String(), fmt.Sprintf("%s:%d", ip.String(), p), nil
}

type pairingReply struct {
	Address string `json:"address"`
	Pin     string `json:"pin"`
	Proof   string `json:"proof"`
}

func serveManualPair(conn net.Conn, key, address, pin string) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReaderSize(conn, 128).ReadSlice('\n')
	parts := strings.Fields(string(line))
	if err != nil || len(line) > 80 || len(parts) != 2 || parts[0] != "YLM2" {
		return
	}
	nonce, err := hex.DecodeString(parts[1])
	if err != nil || len(nonce) != 32 {
		return
	}
	json.NewEncoder(conn).Encode(pairingReply{
		Address: address,
		Pin:     pin,
		Proof:   pairingProof(key, parts[1], address, pin),
	})
}

func pairManual(pairAddress, displayedCode string) (Node, error) {
	host, endpoint, err := privateEndpoint(pairAddress, true)
	if err != nil {
		return Node{}, err
	}
	key, err := canonicalManual(displayedCode)
	if err != nil {
		return Node{}, err
	}
	nonceBytes := make([]byte, 32)
	if _, err = io.ReadFull(rand.Reader, nonceBytes); err != nil {
		return Node{}, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	conn, err := net.DialTimeout("tcp4", endpoint, 5*time.Second)
	if err != nil {
		return Node{}, fmt.Errorf("Nie można połączyć z adresem parowania: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(6 * time.Second))
	if _, err = fmt.Fprintf(conn, "YLM2 %s\n", nonce); err != nil {
		return Node{}, err
	}
	line, err := bufio.NewReaderSize(conn, 2048).ReadSlice('\n')
	if err != nil || len(line) > 1536 {
		return Node{}, errors.New("Worker nie zwrócił poprawnej odpowiedzi parowania")
	}
	var reply pairingReply
	if err = json.Unmarshal(line, &reply); err != nil {
		return Node{}, errors.New("Worker nie zwrócił poprawnej odpowiedzi parowania")
	}
	tlsHost, tlsEndpoint, err := privateEndpoint(reply.Address, true)
	if err != nil || tlsHost != host {
		return Node{}, errors.New("Worker zwrócił inny adres sieciowy")
	}
	pin, pinErr := hex.DecodeString(reply.Pin)
	proof, proofErr := hex.DecodeString(reply.Proof)
	want, wantErr := hex.DecodeString(pairingProof(key, nonce, tlsEndpoint, strings.ToLower(reply.Pin)))
	if pinErr != nil || len(pin) != sha256.Size || proofErr != nil || wantErr != nil || len(proof) != sha256.Size || subtle.ConstantTimeCompare(proof, want) != 1 {
		return Node{}, errors.New("Kod parowania nie pasuje do workera")
	}
	return Node{Address: tlsEndpoint, pin: strings.ToLower(reply.Pin), secret: derivedSessionSecret(key)}, nil
}
