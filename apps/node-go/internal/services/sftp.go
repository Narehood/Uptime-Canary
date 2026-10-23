package services

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SFTPConfig struct {
	Address              string `json:"address"`
	Username             string `json:"username"`
	Password             string `json:"password,omitempty"`
	PrivateKeyPEM        string `json:"privateKeyPem,omitempty"`
	PrivateKeyPassphrase string `json:"privateKeyPassphrase,omitempty"`
	HostKeySHA256        string `json:"hostKeySha256,omitempty"`
	KnownHosts           string `json:"knownHosts,omitempty"` // inline OpenSSH exact-host entries; no file paths
	UnsafeIgnoreHostKey  bool   `json:"unsafeIgnoreHostKey,omitempty"`
	Path                 string `json:"path,omitempty"`
}

func (c SFTPConfig) validate() error {
	if err := validAddress(c.Address); err != nil {
		return err
	}
	if c.Username == "" || len(c.Username) > 1024 || len(c.Password) > 4096 || len(c.PrivateKeyPEM) > maxPEM || len(c.PrivateKeyPassphrase) > 4096 || len(c.KnownHosts) > maxPEM || len(c.Path) > 4096 || strings.ContainsRune(c.Path, 0) {
		return errors.New("invalid SFTP options or size limit")
	}
	if (c.Password == "") == (c.PrivateKeyPEM == "") {
		return errors.New("SFTP requires exactly one password or private key")
	}
	if c.PrivateKeyPEM == "" && c.PrivateKeyPassphrase != "" {
		return errors.New("SFTP key passphrase requires a private key")
	}
	if c.PrivateKeyPEM != "" {
		if err := validateSSHKeyEnvelope(c.PrivateKeyPEM); err != nil {
			return err
		}
	}
	_, err := c.hostKeyCallback()
	return err
}
func (c SFTPConfig) auth() (ssh.AuthMethod, error) {
	if c.Password != "" {
		return ssh.Password(c.Password), nil
	}
	var signer ssh.Signer
	var err error
	if c.PrivateKeyPassphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(c.PrivateKeyPEM), []byte(c.PrivateKeyPassphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(c.PrivateKeyPEM))
	}
	if err != nil {
		return nil, errors.New("invalid SFTP private key or passphrase")
	}
	return ssh.PublicKeys(signer), nil
}
func (c SFTPConfig) hostKeyCallback() (ssh.HostKeyCallback, error) {
	n := 0
	for _, ok := range []bool{c.HostKeySHA256 != "", c.KnownHosts != "", c.UnsafeIgnoreHostKey} {
		if ok {
			n++
		}
	}
	if n != 1 {
		return nil, errors.New("SFTP requires exactly one pinned host key policy")
	}
	if c.UnsafeIgnoreHostKey {
		return ssh.InsecureIgnoreHostKey(), nil
	} // explicit unsafe operator opt-in
	if c.HostKeySHA256 != "" {
		if !strings.HasPrefix(c.HostKeySHA256, "SHA256:") {
			return nil, errors.New("invalid SFTP SHA256 fingerprint")
		}
		v, e := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(c.HostKeySHA256, "SHA256:"))
		if e != nil || len(v) != 32 {
			return nil, errors.New("invalid SFTP SHA256 fingerprint")
		}
		return func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if subtle.ConstantTimeCompare([]byte(ssh.FingerprintSHA256(k)), []byte(c.HostKeySHA256)) != 1 {
				return errors.New("SFTP host key mismatch")
			}
			return nil
		}, nil
	}
	name := knownhosts.Normalize(c.Address)
	var keys []ssh.PublicKey
	rest := []byte(c.KnownHosts)
	for len(bytes.TrimSpace(rest)) > 0 {
		marker, hosts, key, _, next, err := ssh.ParseKnownHosts(rest)
		if err == io.EOF {
			break
		}
		if err != nil || marker != "" {
			return nil, errors.New("invalid SFTP known_hosts entry or unsupported marker")
		}
		rest = next
		for _, host := range hosts {
			if strings.ContainsAny(host, "*?!|") {
				return nil, errors.New("SFTP known_hosts requires exact unhashed host entries")
			}
			if host == name {
				keys = append(keys, key)
			}
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("SFTP known_hosts has no matching host and port")
	}
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		for _, k := range keys {
			if bytes.Equal(k.Marshal(), key.Marshal()) {
				return nil
			}
		}
		return errors.New("SFTP host key mismatch")
	}, nil
}
func checkSFTP(ctx context.Context, c SFTPConfig) Result {
	auth, err := c.auth()
	if err != nil {
		return down("invalid SFTP private key or passphrase")
	}
	hostKey, _ := c.hostKeyCallback()
	raw, err := dial(ctx, "tcp", c.Address)
	if err != nil {
		return down("SFTP connection failed")
	}
	defer raw.Close()
	cc, chans, reqs, err := ssh.NewClientConn(raw, c.Address, &ssh.ClientConfig{User: c.Username, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: hostKey})
	if err != nil {
		return down("SFTP SSH handshake, host key, or authentication failed")
	}
	client := ssh.NewClient(cc, chans, reqs)
	defer client.Close()
	// Only the sftp subsystem is requested. No shell, command, or file write.
	fc, err := sftp.NewClient(client, sftp.MaxConcurrentRequestsPerFile(1))
	if err != nil {
		return down("SFTP subsystem negotiation failed")
	}
	defer fc.Close()
	if c.Path != "" {
		if _, err := fc.Stat(c.Path); err != nil {
			return down("SFTP requested path is unavailable")
		}
	}
	return up("SFTP authenticated subsystem check succeeded")
}

// Check encrypted OpenSSH KDF cost before the SSH library performs any work.
// This prevents configured key data from requesting unbounded bcrypt rounds.
func validateSSHKeyEnvelope(value string) error {
	block, rest := pem.Decode([]byte(value))
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("invalid SFTP private key PEM")
	}
	if block.Type != "OPENSSH PRIVATE KEY" {
		switch block.Type {
		case "RSA PRIVATE KEY", "EC PRIVATE KEY", "DSA PRIVATE KEY", "PRIVATE KEY":
			return nil
		}
		return errors.New("unsupported SFTP private key PEM")
	}
	b := block.Bytes
	magic := []byte("openssh-key-v1\x00")
	if !bytes.HasPrefix(b, magic) {
		return errors.New("invalid OpenSSH private key envelope")
	}
	b = b[len(magic):]
	field := func() ([]byte, bool) {
		if len(b) < 4 {
			return nil, false
		}
		n := uint64(binary.BigEndian.Uint32(b))
		if n > uint64(len(b)-4) {
			return nil, false
		}
		v := b[4 : 4+int(n)]
		b = b[4+int(n):]
		return v, true
	}
	cipher, ok := field()
	if !ok {
		return errors.New("invalid OpenSSH cipher field")
	}
	kdf, ok := field()
	if !ok {
		return errors.New("invalid OpenSSH KDF field")
	}
	options, ok := field()
	if !ok {
		return errors.New("invalid OpenSSH KDF options")
	}
	if string(kdf) == "none" && string(cipher) == "none" && len(options) == 0 {
		return nil
	}
	if string(kdf) != "bcrypt" || len(options) < 8 {
		return errors.New("unsupported OpenSSH KDF")
	}
	n := uint64(binary.BigEndian.Uint32(options))
	if n > 64 || n+8 != uint64(len(options)) {
		return errors.New("invalid OpenSSH bcrypt salt")
	}
	rounds := binary.BigEndian.Uint32(options[4+int(n):])
	if rounds == 0 || rounds > 32 {
		return errors.New("OpenSSH bcrypt rounds must be between one and 32")
	}
	return nil
}
