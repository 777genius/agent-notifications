package sharedrpc

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"time"
)

var errAuthentication = errors.New("shared_authentication_failed")

const authenticationTimeout = 3 * time.Second
const protocolALPN = "agent-notify-rpc-v1"

// AuthenticateClient returns a mutually authenticated, encrypted TLS connection.
// The caller MUST use the returned connection for all RPC I/O. Identity is the
// trusted binding/runtime identity, never model input. Token contains 32 random
// bytes; neither it nor identity is transmitted. Caller closes raw on failure.
func AuthenticateClient(ctx context.Context, raw net.Conn, identity string, token []byte) (net.Conn, error) {
	config, err := authenticationConfig(identity, token, true)
	if err != nil {
		return nil, errAuthentication
	}
	return authenticate(ctx, raw, config, true)
}

func authenticateServer(ctx context.Context, raw net.Conn, config *tls.Config) (net.Conn, error) {
	return authenticate(ctx, raw, config, false)
}

func authenticate(ctx context.Context, raw net.Conn, config *tls.Config, client bool) (net.Conn, error) {
	if ctx == nil || raw == nil || config == nil {
		return nil, errAuthentication
	}
	cleanup, err := boundConnection(ctx, raw, authenticationTimeout)
	if err != nil {
		return nil, errAuthentication
	}
	defer cleanup()
	handshakeCtx, cancel := context.WithTimeout(ctx, authenticationTimeout)
	defer cancel()
	var secured *tls.Conn
	if client {
		secured = tls.Client(raw, config)
	} else {
		secured = tls.Server(raw, config)
	}
	if secured.HandshakeContext(handshakeCtx) != nil {
		return nil, errAuthentication
	}
	return &securedConnection{Conn: secured, raw: raw}, nil
}

// Calls have one bounded response; close the underlying transport immediately
// rather than waiting for a TLS close-notify write to a disconnected bridge.
type securedConnection struct {
	*tls.Conn
	raw net.Conn
}

func (c *securedConnection) Close() error { return c.raw.Close() }

func validCredentials(identity string, token []byte) bool {
	return len(identity) > 0 && len(identity) <= 1024 && len(token) == 32
}

// TLS replaces an unauthenticated local PKI with a binding/runtime-specific SPKI
// pin. Distinct role keys also prevent reflection of a server cert as a client.
func authenticationConfig(identity string, token []byte, client bool) (*tls.Config, error) {
	if !validCredentials(identity, token) {
		return nil, errAuthentication
	}
	ownRole, peerRole := "server", "client"
	ownUsage, peerUsage := x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth
	if client {
		ownRole, peerRole = peerRole, ownRole
		ownUsage, peerUsage = peerUsage, ownUsage
	}
	key := roleKey(token, ownRole, identity)
	public := key.Public().(ed25519.PublicKey)
	serial := sha256.Sum256(public)
	template := &x509.Certificate{
		SerialNumber: new(big.Int).Add(new(big.Int).SetBytes(serial[:16]), big.NewInt(1)),
		Subject:      pkix.Name{CommonName: "agent-notifications local " + ownRole},
		NotBefore:    time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{ownUsage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
	if err != nil {
		return nil, errAuthentication
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, errAuthentication
	}
	peerKey := roleKey(token, peerRole, identity).Public()
	peerSPKI, err := x509.MarshalPKIXPublicKey(peerKey)
	if err != nil {
		return nil, errAuthentication
	}
	config := &tls.Config{
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		NextProtos:             []string{protocolALPN},
		Certificates:           []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}},
		SessionTicketsDisabled: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != protocolALPN || len(state.PeerCertificates) != 1 {
				return errAuthentication
			}
			peer := state.PeerCertificates[0]
			if !hmac.Equal(peer.RawSubjectPublicKeyInfo, peerSPKI) || len(peer.ExtKeyUsage) != 1 || peer.ExtKeyUsage[0] != peerUsage {
				return errAuthentication
			}
			return nil
		},
	}
	if client {
		// Exact SPKI, role EKU, TLS version and ALPN verification above replaces
		// hostname/CA validation for this private, non-network-PKI channel.
		config.InsecureSkipVerify = true
	} else {
		config.ClientAuth = tls.RequireAnyClientCert
	}
	return config, nil
}

func roleKey(token []byte, role, identity string) ed25519.PrivateKey {
	mac := hmac.New(sha256.New, token)
	_, _ = mac.Write([]byte("agent-notifications/sharedrpc/tls13-key/v1"))
	var size [4]byte
	for _, field := range []string{role, identity} {
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		_, _ = mac.Write(size[:])
		_, _ = mac.Write([]byte(field))
	}
	return ed25519.NewKeyFromSeed(mac.Sum(nil))
}

// boundConnection also interrupts I/O for cancellation without a deadline.
func boundConnection(ctx context.Context, conn net.Conn, maximum time.Duration) (func(), error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	end := time.Now().Add(maximum)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(end) {
		end = deadline
	}
	if err := conn.SetDeadline(end); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return func() { stop(); _ = conn.SetDeadline(time.Time{}) }, nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) != 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
