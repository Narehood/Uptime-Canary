// Package canary implements the independent headless monitoring runtime.
package canary

import (
	"context"
	"net/http"
	"time"
	"uptime-canary/apps/node-go/internal/browsermonitor"
	"uptime-canary/apps/node-go/internal/checks"
	"uptime-canary/apps/node-go/internal/gamedigmonitor"
	"uptime-canary/apps/node-go/internal/heartbeat"
	"uptime-canary/apps/node-go/internal/pm2monitor"
	"uptime-canary/apps/node-go/internal/rollup"
	"uptime-canary/apps/node-go/internal/services"
	"uptime-canary/apps/node-go/internal/steam"
)

type MonitorInput struct {
	Name                string                    `json:"name"`
	Description         string                    `json:"description,omitempty"`
	Type                string                    `json:"type"`
	Target              string                    `json:"target"`
	IntervalMs          int64                     `json:"intervalMs"`
	InternalOnly        bool                      `json:"internalOnly,omitempty"`
	TimeoutMs           int64                     `json:"timeoutMs"`
	NodeIDs             []string                  `json:"nodeIds"`
	Enabled             bool                      `json:"enabled"`
	ExpectedStatusCodes []int                     `json:"expectedStatusCodes,omitempty"`
	WebhookURL          string                    `json:"webhookUrl,omitempty"`
	NotificationIDs     []string                  `json:"notificationIds,omitempty"`
	RepeatAfterMs       *int64                    `json:"repeatAfterMs,omitempty"`
	GroupID             string                    `json:"groupId,omitempty"`
	HTTP                *checks.HTTPConfig        `json:"http,omitempty"`
	TLS                 *TLSMonitorOptions        `json:"tls,omitempty"`
	DNS                 *checks.DNSConfig         `json:"dns,omitempty"`
	WebSocket           *checks.WebSocketConfig   `json:"websocket,omitempty"`
	SMTP                *checks.SMTPConfig        `json:"smtp,omitempty"`
	UpsideDown          bool                      `json:"upsideDown,omitempty"`
	Retries             int                       `json:"retries,omitempty"`
	RetryIntervalMs     *int64                    `json:"retryIntervalMs,omitempty"`
	Service             *ServiceMonitorOptions    `json:"service,omitempty"`
	Manual              *ManualMonitorOptions     `json:"manual,omitempty"`
	Ping                *PingMonitorOptions       `json:"ping,omitempty"`
	Tags                []MonitorTag              `json:"tags,omitempty"`
	Push                *PushMonitorOptions       `json:"push,omitempty"`
	Globalping          *GlobalpingMonitorOptions `json:"globalping,omitempty"`
	Steam               *SteamMonitorOptions      `json:"steam,omitempty"`
	PM2                 *PM2MonitorOptions        `json:"pm2,omitempty"`
	Browser             *BrowserMonitorOptions    `json:"browser,omitempty"`
	GameDig             *GameDigMonitorOptions    `json:"gamedig,omitempty"`
}

type SteamMonitorOptions = steam.Options
type PM2MonitorOptions = pm2monitor.Options
type BrowserMonitorOptions = browsermonitor.Options
type GameDigMonitorOptions = gamedigmonitor.Options

type PushMonitorOptions struct {
	Token    string `json:"token"`
	AllowGET bool   `json:"allowGet,omitempty"`
}

type PingMonitorOptions struct {
	Count  int    `json:"count,omitempty"`
	Size   *int   `json:"size,omitempty"`
	Family string `json:"family,omitempty"`
}

type ManualMonitorOptions struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

type ServiceMonitorOptions struct {
	Docker    *services.DockerConfig   `json:"docker,omitempty"`
	Redis     *services.RedisConfig    `json:"redis,omitempty"`
	Postgres  *services.SQLConfig      `json:"postgres,omitempty"`
	MySQL     *services.SQLConfig      `json:"mysql,omitempty"`
	MQTT      *services.MQTTConfig     `json:"mqtt,omitempty"`
	SFTP      *services.SFTPConfig     `json:"sftp,omitempty"`
	MongoDB   *services.MongoDBConfig  `json:"mongodb,omitempty"`
	SQLServer *services.SQLConfig      `json:"sqlserver,omitempty"`
	NTP       *NTPMonitorOptions       `json:"ntp,omitempty"`
	GRPC      *services.GRPCConfig     `json:"grpc,omitempty"`
	SNMP      *services.SNMPConfig     `json:"snmp,omitempty"`
	RADIUS    *services.RADIUSConfig   `json:"radius,omitempty"`
	SIP       *services.SIPConfig      `json:"sip,omitempty"`
	RabbitMQ  *services.RabbitMQConfig `json:"rabbitmq,omitempty"`
	Kafka     *services.KafkaConfig    `json:"kafka,omitempty"`
	Oracle    *services.SQLConfig      `json:"oracle,omitempty"`
}

type NTPMonitorOptions struct {
	Address                   string `json:"address"`
	StratumThreshold          uint8  `json:"stratumThreshold,omitempty"`
	OffsetThresholdMs         int64  `json:"offsetThresholdMs,omitempty"`
	RootDispersionThresholdMs int64  `json:"rootDispersionThresholdMs,omitempty"`
	LatencyThresholdMs        int64  `json:"latencyThresholdMs,omitempty"`
}

type TLSMonitorOptions struct {
	ServerName         string  `json:"serverName,omitempty"`
	CAPEM              string  `json:"caPem,omitempty"`
	ClientCertPEM      string  `json:"clientCertPem,omitempty"`
	ClientKeyPEM       string  `json:"clientKeyPem,omitempty"`
	MinValidityDays    float64 `json:"minValidityDays,omitempty"`
	IgnoreVerification bool    `json:"ignoreVerification,omitempty"`
}
type Monitor struct {
	MonitorInput
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	UpdatedAt int64  `json:"updatedAt"`
	Deleted   bool   `json:"deleted,omitempty"`
	// Omitted on a new check generation; otherwise names the immutable revision
	// that began this generation. Metadata edits inherit it through the DAG.
	CheckGeneration string `json:"checkGeneration,omitempty"`
	// Generation anchor for missed passive heartbeats. Metadata edits retain it.
	PushStartedAt int64 `json:"pushStartedAt,omitempty"`
}
type GroupInput struct {
	Name            string   `json:"name"`
	NodeIDs         []string `json:"nodeIds"`
	OutageMonitorID string   `json:"outageMonitorId,omitempty"`
	GraceMs         int64    `json:"graceMs"`
	ParentID        string   `json:"parentId,omitempty"`
}
type Group struct {
	GroupInput
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	UpdatedAt int64  `json:"updatedAt"`
	Deleted   bool   `json:"deleted,omitempty"`
}
type Observation struct {
	MonitorID           string                     `json:"monitorId"`
	NodeID              string                     `json:"nodeId"`
	State               string                     `json:"state"`
	CheckedAt           int64                      `json:"checkedAt"`
	LatencyMs           float64                    `json:"latencyMs"`
	LatencyAvailable    *bool                      `json:"latencyAvailable,omitempty"`
	Message             string                     `json:"message"`
	RawState            string                     `json:"rawState,omitempty"`
	ConsecutiveFailures int                        `json:"consecutiveFailures,omitempty"`
	StatusCode          int                        `json:"statusCode,omitempty"`
	Certificate         *checks.CertificateSummary `json:"certificate,omitempty"`
}
type Sample struct {
	Observation
	Sequence        int64  `json:"sequence"`
	Revision        string `json:"revision"`
	DownSince       *int64 `json:"downSince"`
	CheckGeneration string `json:"checkGeneration,omitempty"`
}
type MonitorView struct {
	Monitor             Monitor       `json:"monitor"`
	State               string        `json:"state"`
	Observations        []Observation `json:"observations"`
	UnknownNodes        int           `json:"unknownNodes"`
	ConfigConflict      bool          `json:"configConflict"`
	SuppressedByGroupID string        `json:"suppressedByGroupId,omitempty"`
	SuppressionReason   string        `json:"suppressionReason,omitempty"`
	MaintenanceIDs      []string      `json:"maintenanceIds,omitempty"`
}
type GroupView struct {
	Group             Group          `json:"group"`
	State             string         `json:"state"`
	ConfigConflict    bool           `json:"configConflict"`
	SuppressionActive bool           `json:"suppressionActive"`
	Reason            string         `json:"reason"`
	ReachableNodes    int            `json:"reachableNodes"`
	TotalNodes        int            `json:"totalNodes"`
	HealthState       string         `json:"healthState"`
	Health            *rollup.Result `json:"health,omitempty"`
	HealthReason      string         `json:"healthReason,omitempty"`
}
type NodeView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url,omitempty"`
	State      string `json:"state"`
	LastSeenAt *int64 `json:"lastSeenAt"`
}
type IncidentView struct {
	ID                  string   `json:"id"`
	MonitorID           string   `json:"monitorId"`
	MonitorName         string   `json:"monitorName"`
	OpenedAt            int64    `json:"openedAt"`
	ResolvedAt          *int64   `json:"resolvedAt"`
	NotifiedAt          *int64   `json:"notifiedAt"`
	RecoveryNotifiedAt  *int64   `json:"recoveryNotifiedAt"`
	SuppressedByGroupID string   `json:"suppressedByGroupId,omitempty"`
	SuppressionReason   string   `json:"suppressionReason,omitempty"`
	RetiredBy           string   `json:"retiredBy,omitempty"`
	RetirementReason    string   `json:"retirementReason,omitempty"`
	MaintenanceIDs      []string `json:"maintenanceIds,omitempty"`
	MaintenanceComplete bool     `json:"maintenanceComplete,omitempty"`
}
type NotificationView struct {
	ID            string `json:"id"`
	MonitorID     string `json:"monitorId"`
	IncidentID    string `json:"incidentId"`
	Kind          string `json:"kind"`
	NodeID        string `json:"nodeId"`
	SentAt        int64  `json:"sentAt"`
	DestinationID string `json:"destinationId,omitempty"`
	ChannelID     string `json:"channelId,omitempty"`
	Provider      string `json:"provider,omitempty"`
}
type AccountView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	PasswordEnabled bool   `json:"passwordEnabled"`
	MFAEnabled      bool   `json:"mfaEnabled"`
}
type account struct {
	AccountView
	Hash               string `json:"hash"`
	PasswordSalt       string `json:"passwordSalt,omitempty"`
	PasswordHash       string `json:"passwordHash,omitempty"`
	PasswordVersion    int    `json:"passwordVersion,omitempty"`
	TOTPSecret         string `json:"totpSecret,omitempty"`
	TOTPLastStep       int64  `json:"totpLastStep,omitempty"`
	PendingTOTP        string `json:"pendingTotp,omitempty"`
	PendingTOTPExpires int64  `json:"pendingTotpExpires,omitempty"`
	AuthVersion        uint64 `json:"authVersion,omitempty"`
}
type Identity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Overview struct {
	Build         *BinaryIdentity    `json:"build,omitempty"`
	Node          Identity           `json:"node"`
	Now           int64              `json:"now"`
	Monitors      []MonitorView      `json:"monitors"`
	Groups        []GroupView        `json:"groups"`
	Nodes         []NodeView         `json:"nodes"`
	Incidents     []IncidentView     `json:"incidents"`
	Notifications []NotificationView `json:"notifications"`
	Maintenance   []MaintenanceView  `json:"maintenance"`
	User          struct {
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"user"`
}
type Version struct {
	Monitor   Monitor  `json:"monitor"`
	Ancestors []string `json:"ancestors"`
	EdgeID    string   `json:"edgeId,omitempty"`
}
type GroupVersion struct {
	Group     Group    `json:"group"`
	Ancestors []string `json:"ancestors"`
	EdgeID    string   `json:"edgeId,omitempty"`
}
type Incident struct {
	uncertainties []incidentUncertainty
	// Commits the complete paged run, alias and predecessor evidence. The
	// inline arrays are summaries when this is set; never reconcile them alone.
	EvidenceRoot string             `json:"evidenceRoot,omitempty"`
	ID           string             `json:"id"`
	Aliases      []string           `json:"aliases"`
	MonitorID    string             `json:"monitorId"`
	MonitorName  string             `json:"monitorName"`
	Revision     string             `json:"revision"`
	OpenedAt     int64              `json:"openedAt"`
	ResolvedAt   *int64             `json:"resolvedAt"`
	Runs         map[string][]int64 `json:"runs"`
	Closed       map[string]int64   `json:"closed"`
	Predecessors []string           `json:"predecessors"`
	// Revision identifies the check generation, not each metadata revision.
	// Retirement is terminal and never authorizes a recovery webhook.
	RetiredBy string `json:"retiredBy,omitempty"`
	// A completed, never-delivered suppressed episode must never replay later.
	SuppressedByGroupID   string `json:"suppressedByGroupId,omitempty"`
	SuppressionReason     string `json:"suppressionReason,omitempty"`
	SuppressionReleasedAt *int64 `json:"suppressionReleasedAt,omitempty"`
	SuppressedAt          *int64 `json:"suppressedAt,omitempty"`
	SuppressedComplete    bool   `json:"suppressedComplete,omitempty"`
	// Bounded summary of the durable, separately paged NotificationIntent facts.
	// New audience is captured only while fresh down evidence keeps it open.
	DeliveryTargets []string `json:"deliveryTargets"`
	// Planned work holds delivery, while observations and incident history remain.
	MaintenanceIDs        []string `json:"maintenanceIds,omitempty"`
	MaintenanceStartedAt  *int64   `json:"maintenanceStartedAt,omitempty"`
	MaintenanceReleasedAt *int64   `json:"maintenanceReleasedAt,omitempty"`
	MaintenanceComplete   bool     `json:"maintenanceComplete,omitempty"`
}
type Receipt struct {
	NotificationView
	Slot int64 `json:"slot"`
}
type Snapshot struct {
	Protocol       int                  `json:"protocol"`
	Node           Identity             `json:"node"`
	Versions       []Version            `json:"versions"`
	GroupVersions  []GroupVersion       `json:"groupVersions"`
	Resources      []ResourceVersion    `json:"resources"`
	Intents        []NotificationIntent `json:"intents"`
	Observations   []Sample             `json:"observations"`
	Incidents      []Incident           `json:"incidents"`
	Receipts       []Receipt            `json:"receipts"`
	RevisionEdges  []RevisionEdge       `json:"revisionEdges,omitempty"`
	RevisionClaims []RevisionClaim      `json:"revisionClaims,omitempty"`
	IncidentFacts  []IncidentFact       `json:"incidentFacts,omitempty"`
	EvidenceLinks  []EvidenceLink       `json:"evidenceLinks,omitempty"`
	Heartbeats     []heartbeat.Event    `json:"heartbeats,omitempty"`
	// Protocol 7 exchanges independently mergeable pages. After requests the
	// remote page following that stable key; Next advances this page's sender.
	After               string `json:"after,omitempty"`
	Next                string `json:"next,omitempty"`
	preparedMaintenance compiledMaintenance
}
type attempt struct {
	ID                  string `json:"id"`
	MonitorID           string `json:"monitorId"`
	IncidentID          string `json:"incidentId"`
	Kind                string `json:"kind"`
	Slot                int64  `json:"slot"`
	Failures            int    `json:"failures"`
	NextAt              int64  `json:"nextAt"`
	DestinationID       string `json:"destinationId,omitempty"`
	ChannelID           string `json:"channelId,omitempty"`
	TargetRevision      string `json:"targetRevision,omitempty"`
	Provider            string `json:"provider,omitempty"`
	State               string `json:"state,omitempty"`
	LastAt              int64  `json:"lastAttemptAt,omitempty"`
	HTTPStatus          int    `json:"httpStatus,omitempty"`
	ErrorCode           string `json:"errorCode,omitempty"`
	ProviderID          string `json:"providerId,omitempty"`
	Held                bool   `json:"held,omitempty"`
	ReleasedAt          int64  `json:"releasedAt,omitempty"`
	SuppressionBoundary string `json:"suppressionBoundary,omitempty"`
}
type Peer struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	URL            string `json:"url"`
	TLSFingerprint string `json:"tlsFingerprint,omitempty"`
}
type settings struct {
	Peers               []Peer   `json:"peers"`
	CORSOrigins         []string `json:"corsOrigins"`
	SyncIntervalMs      int64    `json:"syncIntervalMs"`
	NotificationDelayMs int64    `json:"notificationDelayMs"`
	PeerTimeoutMs       int64    `json:"peerTimeoutMs"`
}
type ProbeResult struct {
	State            string
	LatencyMs        float64
	LatencyAvailable *bool
	Message          string
	StatusCode       int
	Screenshot       []byte
	Certificate      *checks.CertificateSummary
}
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Options supplies test seams and deployment settings. Nil pointer settings use
// persisted/default values; Port nil is 3100 and pointer-to-zero is ephemeral.
type Options struct {
	NodeID, NodeName, Host, DBPath, AdminToken, ClusterToken string
	IdentityDirectory                                        string
	TLSMode, DashboardTLSFingerprint                         string
	DashboardURL, DashboardEnrollToken                       string
	Port                                                     *int
	Peers                                                    []Peer
	CORSOrigins                                              []string
	SyncIntervalMs, NotificationDelayMs, PeerTimeoutMs       *int64
	Now                                                      func() int64
	Probe                                                    func(context.Context, Monitor) ProbeResult
	Client, PeerClient                                       Doer
	TickInterval                                             time.Duration
	HistoryLimit                                             int
	HistoryRetention                                         time.Duration
	WebhookTimeout                                           time.Duration
}
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string           { return e.Message }
func fail(status int, message string) error { return &apiError{status, message} }
func Ptr[T any](v T) *T                     { return &v }
