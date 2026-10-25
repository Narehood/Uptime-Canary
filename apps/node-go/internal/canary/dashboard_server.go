package canary

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DashboardHandler serves assets and the control plane from the same origin.
// Runtime flags are generated here, never inferred from an unavailable node.
func DashboardHandler(d *Dashboard, assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		p := r.URL.Path
		if p == "/canary-config.js" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			_, _ = io.WriteString(w, "globalThis.__CANARY_DASHBOARD_GATEWAY__ = true;\nglobalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = true;\n")
			return
		}
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/public/") || p == "/healthz" || p == "/readyz" {
			d.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/internal") || p == "/api" || (r.Method != "GET" && r.Method != "HEAD") || path.Clean(p) != p || strings.Contains(p, "\\") {
			http.NotFound(w, r)
			return
		}
		if p == "/" {
			p = "/index.html"
		}
		name := strings.TrimPrefix(p, "/")
		info, err := fs.Stat(assets, name)
		if err != nil || info.IsDir() {
			if strings.HasPrefix(p, "/assets/") || strings.Contains(path.Base(p), ".") {
				http.NotFound(w, r)
				return
			}
			p = "/index.html"
		}
		if p == "/index.html" {
			b, err := fs.ReadFile(assets, "index.html")
			if err != nil {
				errorReply(w, fail(503, "Dashboard assets unavailable"))
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.Method == "HEAD" {
				w.WriteHeader(200)
				return
			}
			// The server-owned HTML marker keeps the client on its dashboard
			// account flow even if a proxy blocks the external runtime script.
			b = []byte(strings.Replace(string(b), "<html", `<html data-canary-dashboard="control-plane"`, 1))
			_, _ = w.Write(b)
			return
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Path = p
		clone.URL = &u
		if strings.HasPrefix(p, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, clone)
	})
}

func dashboardPrivateDirectory(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errors.New("Dashboard directories must be clean absolute paths")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("Cannot create dashboard directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Dashboard directory must be a real directory")
	}
	// Chmod is meaningful on Unix and preserves the Windows account's existing
	// ACL semantics; it does not claim to replace Windows ACL administration.
	if err := os.Chmod(dir, 0700); err != nil {
		return errors.New("Cannot restrict dashboard directory permissions")
	}
	return nil
}

func dashboardPrivateValue(filename string) (string, error) {
	if info, err := os.Lstat(filename); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 256 || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("Invalid dashboard identity file")
		}
		b, err := os.ReadFile(filename)
		if err != nil {
			return "", errors.New("Cannot read dashboard identity file")
		}
		value := strings.TrimSpace(string(b))
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			return "", errors.New("Invalid dashboard identity file")
		}
		if err = os.Chmod(filename, 0600); err != nil {
			return "", errors.New("Cannot restrict dashboard identity file permissions")
		}
		return value, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("Cannot inspect dashboard identity file")
	}
	value, err := randomCredential(32)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", errors.New("Cannot exclusively create dashboard identity file")
	}
	_, writeErr := io.WriteString(f, value+"\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return "", errors.New("Cannot persist dashboard identity file; preserve it for operator recovery")
	}
	return value, nil
}

// RunDashboard owns a distinct account database and identity volume. It never
// opens the configured monitoring node's database or starts a probe scheduler.
func RunDashboard(ctx context.Context, env func(string) string, log io.Writer) error {
	value := func(key, fallback string) string {
		if v := env(key); v != "" {
			return v
		}
		return fallback
	}
	data := value("CANARY_DASHBOARD_DATA", "/data")
	identity := value("CANARY_DASHBOARD_IDENTITY", "/identity")
	if data == identity {
		return errors.New("Keep dashboard identity/key material outside the database backup directory")
	}
	if err := dashboardPrivateDirectory(data); err != nil {
		return err
	}
	if err := dashboardPrivateDirectory(identity); err != nil {
		return err
	}
	// Never create a replacement key when this database already exists. Restores
	// need their retained key, not a silently generated incompatible one.
	if _, err := os.Lstat(filepath.Join(data, "accounts.db")); err == nil {
		for _, name := range []string{"dashboard.key", "setup.code"} {
			if _, err = os.Lstat(filepath.Join(identity, name)); err != nil {
				return errors.New("Dashboard database exists without its retained identity files; restore the matching identity volume")
			}
		}
	}
	keyText, err := dashboardPrivateValue(filepath.Join(identity, "dashboard.key"))
	if err != nil {
		return err
	}
	setup, err := dashboardPrivateValue(filepath.Join(identity, "setup.code"))
	if err != nil {
		return err
	}
	key, _ := base64.RawURLEncoding.DecodeString(keyText)
	origins := []string{}
	for _, origin := range strings.Split(value("CANARY_DASHBOARD_ORIGINS", value("CANARY_CORS_ORIGINS", "http://localhost:18347,http://127.0.0.1:18347")), ",") {
		canonical, e := dashboardNodeURL(strings.TrimSpace(origin))
		if e != nil || canonical != strings.TrimSpace(origin) {
			return errors.New("Dashboard allowed origins must be exact HTTP(S) origins without trailing paths")
		}
		origins = append(origins, canonical)
	}
	accounts, err := NewDashboardAccounts(DashboardAccountOptions{Directory: data, SetupToken: setup, CORSOrigins: origins, IdentityDirectory: filepath.Join(identity, "auth-origin")})
	if err != nil {
		return err
	}
	defer accounts.Close()
	d, err := NewDashboard(DashboardOptions{Accounts: accounts, Key: key, CORSOrigins: origins, LocalNodeURL: env("CANARY_LOCAL_NODE_URL"), LocalNodeToken: env("CANARY_LOCAL_NODE_TOKEN")})
	if err != nil {
		return err
	}
	defer d.Close()
	port, err := strconv.Atoi(value("CANARY_DASHBOARD_PORT", "8080"))
	if err != nil || port < 1 || port > 65535 {
		return errors.New("Invalid dashboard port")
	}
	assets := value("CANARY_DASHBOARD_ASSETS", "/app/web")
	if _, err = os.Stat(filepath.Join(assets, "index.html")); err != nil {
		return errors.New("Dashboard assets are missing")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(value("CANARY_DASHBOARD_HOST", "0.0.0.0"), strconv.Itoa(port)))
	if err != nil {
		return errors.New("Dashboard listener failed to start")
	}
	defer listener.Close()
	server := &http.Server{Handler: DashboardHandler(d, os.DirFS(assets)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	background, cancel := context.WithCancel(ctx)
	done := d.Start(background)
	defer func() { cancel(); <-done }()
	if !accounts.Initialized() {
		fmt.Fprintf(log, "Dashboard first-owner setup code: %s\nThis code stops working after the first owner is created. Keep /identity separate from database backups.\n", setup)
	}
	fmt.Fprintln(log, "Uptime-Canary dashboard ready; monitoring nodes remain independent")
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		err := server.Shutdown(shutdown)
		if err != nil {
			_ = server.Close()
		}
		<-errCh
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("Dashboard HTTP server stopped")
	}
}
