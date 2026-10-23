package services

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func testSSHKey(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	return signer, string(pem.EncodeToMemory(block))
}
func sftpFixture(t *testing.T, host, userKey ssh.Signer, variant string) string {
	return fixture(t, func(raw net.Conn) error {
		config := &ssh.ServerConfig{PasswordCallback: func(m ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if variant != "auth-refused" && m.User() == "monitor" && string(p) == "fixture-secret" {
				return nil, nil
			}
			return nil, errors.New("fixture-secret rejected")
		}, PublicKeyCallback: func(m ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if userKey != nil && m.User() == "monitor" && bytes.Equal(key.Marshal(), userKey.PublicKey().Marshal()) {
				return nil, nil
			}
			return nil, errors.New("key refused")
		}}
		config.AddHostKey(host)
		conn, channels, requests, err := ssh.NewServerConn(raw, config)
		if err != nil {
			return nil
		}
		defer conn.Close()
		go ssh.DiscardRequests(requests)
		for ch := range channels {
			if ch.ChannelType() != "session" {
				ch.Reject(ssh.UnknownChannelType, "only session")
				return fmt.Errorf("unexpected SSH channel")
			}
			channel, reqs, err := ch.Accept()
			if err != nil {
				return err
			}
			defer channel.Close()
			for r := range reqs {
				if r.Type != "subsystem" || !bytes.Equal(r.Payload, ssh.Marshal(struct{ Name string }{"sftp"})) {
					r.Reply(false, nil)
					return fmt.Errorf("unexpected SSH request")
				}
				if variant == "subsystem-refused" {
					r.Reply(false, nil)
					return nil
				}
				r.Reply(true, nil)
				if variant == "malformed" {
					_, _ = channel.Write([]byte{0, 0, 0, 0})
					return nil
				}
				handlers := sftp.InMemHandler()
				server := sftp.NewRequestServer(channel, handlers)
				defer server.Close()
				err := server.Serve()
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}
				return nil
			}
		}
		return nil
	})
}
func TestSFTPAuthenticatedLibraryServer(t *testing.T) {
	host, _ := testSSHKey(t)
	userKey, keyPEM := testSSHKey(t)
	for _, tc := range []struct {
		name, path            string
		publicKey, knownHosts bool
	}{
		{"password", "", false, false}, {"path", "/", false, false}, {"private-key", "/", true, false}, {"known-hosts", "/", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address := sftpFixture(t, host, userKey, "success")
			c := &SFTPConfig{Address: address, Username: "monitor", Password: "fixture-secret", HostKeySHA256: ssh.FingerprintSHA256(host.PublicKey()), Path: tc.path}
			if tc.publicKey {
				c.Password = ""
				c.PrivateKeyPEM = keyPEM
			}
			if tc.knownHosts {
				c.HostKeySHA256 = ""
				c.KnownHosts = knownhosts.Line([]string{address}, host.PublicKey()) + "\n"
			}
			expectState(t, Config{Type: SFTP, Timeout: time.Second, SFTP: c}, "up")
		})
	}
}
func TestSFTPEncryptedPrivateKey(t *testing.T) {
	host, _ := testSSHKey(t)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	userKey, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(key, "fixture", []byte("fixture-secret"))
	if err != nil {
		t.Fatal(err)
	}
	address := sftpFixture(t, host, userKey, "success")
	c := &SFTPConfig{Address: address, Username: "monitor", PrivateKeyPEM: string(pem.EncodeToMemory(block)), PrivateKeyPassphrase: "fixture-secret", HostKeySHA256: ssh.FingerprintSHA256(host.PublicKey())}
	expectState(t, Config{Type: SFTP, Timeout: 2 * time.Second, SFTP: c}, "up")
}
func TestSFTPRefusalsAndHostKeyVerification(t *testing.T) {
	host, _ := testSSHKey(t)
	other, _ := testSSHKey(t)
	for _, variant := range []string{"auth-refused", "subsystem-refused", "malformed", "missing-path", "wrong-host-key"} {
		t.Run(variant, func(t *testing.T) {
			address := sftpFixture(t, host, nil, variant)
			c := &SFTPConfig{Address: address, Username: "monitor", Password: "fixture-secret", HostKeySHA256: ssh.FingerprintSHA256(host.PublicKey())}
			if variant == "missing-path" {
				c.Path = "/private-value"
			}
			if variant == "wrong-host-key" {
				c.HostKeySHA256 = ssh.FingerprintSHA256(other.PublicKey())
			}
			expectState(t, Config{Type: SFTP, Timeout: time.Second, SFTP: c}, "down")
		})
	}
	for _, c := range []SFTPConfig{{Address: "localhost:22", Username: "monitor", Password: "fixture-secret"}, {Address: "localhost:22", Username: "monitor", Password: "fixture-secret", HostKeySHA256: "SHA256:invalid"}, {Address: "localhost:22", Username: "monitor", PrivateKeyPEM: "invalid", UnsafeIgnoreHostKey: true}, {Address: "localhost:22", Username: "monitor", Password: "fixture-secret", KnownHosts: knownhosts.Line([]string{"elsewhere:22"}, host.PublicKey())}} {
		if c.validate() == nil {
			t.Fatal("accepted unsafe SFTP options")
		}
	}
}

func TestSFTPOpenSSHKeyKDFCostBound(t *testing.T) {
	field := func(b []byte) []byte {
		out := make([]byte, len(b)+4)
		binary.BigEndian.PutUint32(out, uint32(len(b)))
		copy(out[4:], b)
		return out
	}
	for _, rounds := range []uint32{0, 1, 16, 32, 33, 1 << 30} {
		options := field([]byte("0123456789abcdef"))
		v := make([]byte, 4)
		binary.BigEndian.PutUint32(v, rounds)
		options = append(options, v...)
		data := []byte("openssh-key-v1\x00")
		data = append(data, field([]byte("aes256-ctr"))...)
		data = append(data, field([]byte("bcrypt"))...)
		data = append(data, field(options)...)
		key := pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: data})
		if got := validateSSHKeyEnvelope(string(key)) == nil; got != (rounds >= 1 && rounds <= 32) {
			t.Fatalf("unexpected KDF acceptance for %d rounds", rounds)
		}
	}
}
