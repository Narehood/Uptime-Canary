package canary

// DashboardAccounts reuses the runtime's durable identity state machine in a
// separate identity domain. It never starts a listener, scheduler or peer sync.
// It exposes only accounts/authentication, not the hidden runtime's monitor API.
import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type DashboardAccountOptions struct {
	Directory         string
	SetupToken        string
	CORSOrigins       []string
	IdentityDirectory string
}

type DashboardAccounts struct {
	node        *Node
	setupHash   string
	initialized bool // guarded by node.mu; published only after the transaction
}

func dashboardServiceToken(purpose, setup string) string {
	h := sha256.Sum256([]byte(purpose + "\x00" + setup))
	return hex.EncodeToString(h[:])
}

func NewDashboardAccounts(o DashboardAccountOptions) (*DashboardAccounts, error) {
	if !filepath.IsAbs(o.Directory) || filepath.Clean(o.Directory) != o.Directory {
		return nil, errors.New("Dashboard account directory must be a clean absolute path")
	}
	if err := tokenValue(o.SetupToken); err != nil {
		return nil, errors.New("A private dashboard setup token is required")
	}
	if err := os.MkdirAll(o.Directory, 0700); err != nil {
		return nil, errors.New("Cannot create dashboard account directory")
	}
	info, err := os.Lstat(o.Directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Dashboard account directory must be a real directory")
	}
	serviceToken := dashboardServiceToken("dashboard-identity-bootstrap", o.SetupToken)
	identityDirectory := o.IdentityDirectory
	if identityDirectory == "" {
		identityDirectory = filepath.Join(o.Directory, "auth-origin")
	}
	n, err := New(Options{NodeID: "dashboard-accounts", NodeName: "Dashboard accounts",
		DBPath: filepath.Join(o.Directory, "accounts.db"), IdentityDirectory: identityDirectory,
		AdminToken: serviceToken, ClusterToken: dashboardServiceToken("dashboard-unused-peer-token", o.SetupToken),
		CORSOrigins: o.CORSOrigins})
	if err != nil {
		return nil, err
	}
	initialized, _, err := loadOne[bool](n.store, "settings", "dashboardOwnerInitialized")
	if err != nil {
		_ = n.Stop()
		return nil, errors.New("Cannot load dashboard account initialization")
	}
	if !initialized {
		bootstrap, ok := n.accounts["bootstrap-admin"]
		if !ok || len(n.accounts) != 1 || bootstrap.PasswordEnabled || !matches(bootstrap.Hash, serviceToken) {
			_ = n.Stop()
			return nil, errors.New("This is not an uninitialized dashboard account database")
		}
	} else if _, bootstrap := n.accounts["bootstrap-admin"]; bootstrap {
		_ = n.Stop()
		return nil, errors.New("Initialized dashboard accounts are incomplete; restore or recover the existing owner accounts")
	}
	return &DashboardAccounts{node: n, setupHash: digest(o.SetupToken), initialized: initialized}, nil
}

func (d *DashboardAccounts) Initialized() bool {
	d.node.mu.Lock()
	defer d.node.mu.Unlock()
	return d.initialized
}

func (d *DashboardAccounts) Close() error { return d.node.Stop() }

// Authenticate returns no hashes, credentials or MFA seeds. Callers must invoke
// it again after body I/O and immediately before admitting a protected operation.
func (d *DashboardAccounts) Authenticate(r *http.Request) (AccountView, error) {
	n := d.node
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || n.fault != nil {
		return AccountView{}, fail(503, "Dashboard accounts unavailable")
	}
	if !d.initialized {
		return AccountView{}, fail(401, "Dashboard setup is required")
	}
	a, err := n.authenticate(r)
	return a.AccountView, err
}

func (d *DashboardAccounts) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" {
		if !slices.Contains(d.node.settings.CORSOrigins, origin) {
			errorReply(w, fail(403, "Origin is not allowed"))
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	if r.URL.Path == "/api/auth/status" && r.Method == "GET" {
		n := d.node
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.stopped || n.fault != nil {
			errorReply(w, fail(503, "Dashboard accounts unavailable"))
			return
		}
		password := false
		for _, a := range n.accounts {
			password = password || a.PasswordEnabled
		}
		reply(w, 200, map[string]any{"dashboard": true, "setupRequired": !d.initialized,
			"passwordConfigured": password, "passwordLogin": true, "tokenLogin": d.initialized, "mfaSupported": true})
		return
	}
	if r.URL.Path == "/api/dashboard/setup" && r.Method == "POST" {
		d.setup(w, r)
		return
	}
	if _, recognized := identityFields(r.URL.Path, r.Method); !recognized {
		errorReply(w, fail(404, "Not found"))
		return
	}
	if !d.Initialized() {
		errorReply(w, fail(409, "Create the dashboard owner account first"))
		return
	}
	d.node.ServeHTTP(w, r)
}

func (d *DashboardAccounts) setup(w http.ResponseWriter, r *http.Request) {
	if d.Initialized() {
		errorReply(w, fail(409, "This dashboard already has an owner"))
		return
	}
	b, err := readBounded(r.Body, 8192)
	if err != nil {
		errorReply(w, err)
		return
	}
	in, err := parseIdentity(b, []string{"name", "password", "code"})
	if err != nil {
		errorReply(w, err)
		return
	}
	if !matches(d.setupHash, in.Code) {
		errorReply(w, fail(401, "Dashboard setup code was not accepted"))
		return
	}
	name := strings.TrimSpace(in.Name)
	if err := validText(name, "account name", 128); err != nil {
		errorReply(w, err)
		return
	}
	n := d.node
	release, err := n.passwordWorker()
	if err != nil {
		errorReply(w, err)
		return
	}
	defer release()
	owner, err := setPassword(account{AccountView: AccountView{ID: newID(), Name: name, Role: "admin"}, AuthVersion: 1}, in.Password)
	if err != nil {
		errorReply(w, err)
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if d.initialized {
		errorReply(w, fail(409, "This dashboard already has an owner"))
		return
	}
	if r.Context().Err() != nil {
		errorReply(w, fail(408, "Request cancelled"))
		return
	}
	if err := n.identityTransactionLocked(func(e dbExec) error {
		if err := put(e, "accounts", owner.ID, owner); err != nil {
			return err
		}
		if err := remove(e, "accounts", "bootstrap-admin"); err != nil {
			return err
		}
		return put(e, "settings", "dashboardOwnerInitialized", true)
	}); err != nil {
		errorReply(w, err)
		return
	}
	n.accounts = map[string]account{owner.ID: owner}
	d.initialized = true
	reply(w, 201, owner.AccountView)
}
