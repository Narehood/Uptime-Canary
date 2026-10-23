package canary

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"
	"uptime-canary/apps/node-go/internal/services"
)

func isServiceType(kind string) bool {
	return slices.Contains([]string{"docker", "redis", "postgres", "mysql", "mqtt", "sftp", "mongodb", "sqlserver", "ntp", "grpc", "snmp", "radius", "sip-options", "rabbitmq", "kafka-producer", "oracledb"}, kind)
}

func serviceCheckConfig(m MonitorInput) services.Config {
	c := services.Config{Type: services.Type(m.Type), Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
	if m.Service != nil {
		o := m.Service
		c.Docker, c.Redis, c.Postgres, c.MySQL, c.MQTT, c.SFTP = o.Docker, o.Redis, o.Postgres, o.MySQL, o.MQTT, o.SFTP
		c.MongoDB, c.SQLServer, c.GRPC = o.MongoDB, o.SQLServer, o.GRPC
		c.SNMP, c.RADIUS, c.SIP, c.RabbitMQ = o.SNMP, o.RADIUS, o.SIP, o.RabbitMQ
		c.Kafka, c.Oracle = o.Kafka, o.Oracle
		if o.NTP != nil {
			n := o.NTP
			c.NTP = &services.NTPConfig{Address: n.Address, StratumThreshold: n.StratumThreshold, OffsetThreshold: time.Duration(n.OffsetThresholdMs) * time.Millisecond, RootDispersionThreshold: time.Duration(n.RootDispersionThresholdMs) * time.Millisecond, LatencyThreshold: time.Duration(n.LatencyThresholdMs) * time.Millisecond}
		}
	}
	return c
}

// Public targets contain no private query, DSN credential, topic or key data.
// Endpoint options remain private configuration, redacted for viewer accounts.
func serviceDisplayTarget(c services.Config) string {
	switch c.Type {
	case services.Docker:
		return c.Docker.Container + " at " + c.Docker.Endpoint
	case services.Redis:
		return c.Redis.Address
	case services.Kafka:
		return "Kafka " + c.Kafka.Mode + " at " + c.Kafka.Brokers[0]
	case services.Oracle:
		u, _ := url.Parse(c.Oracle.DSN)
		return "Oracle at " + u.Host
	case services.MQTT:
		return c.MQTT.Address
	case services.SFTP:
		return c.SFTP.Address
	case services.NTP:
		return c.NTP.Address
	case services.GRPC:
		return c.GRPC.Address
	case services.SNMP:
		return c.SNMP.Address
	case services.RADIUS:
		return c.RADIUS.Address
	case services.SIP:
		return c.SIP.Address
	case services.RabbitMQ:
		if c.RabbitMQ.Mode == "amqp" {
			return c.RabbitMQ.Address
		}
		if len(c.RabbitMQ.ManagementURLs) != 0 {
			u, _ := url.Parse(c.RabbitMQ.ManagementURLs[0])
			return "RabbitMQ management at " + u.Host
		}
		return "RabbitMQ management"
	case services.MongoDB:
		u, _ := url.Parse(c.MongoDB.DSN)
		return "MongoDB at " + u.Host
	case services.SQLServer:
		u, _ := url.Parse(c.SQLServer.DSN)
		return "SQL Server at " + u.Host
	case services.Postgres:
		u, _ := url.Parse(c.Postgres.DSN)
		return "PostgreSQL at " + u.Host
	case services.MySQL:
		dsn := c.MySQL.DSN
		start := strings.LastIndex(dsn, "@tcp(") + len("@tcp(")
		if start >= len("@tcp(") {
			if end := strings.IndexByte(dsn[start:], ')'); end >= 0 {
				return "MySQL at " + dsn[start:start+end]
			}
		}
		return "MySQL query"
	}
	return "Service check"
}

func ServiceProbe(ctx context.Context, m Monitor) ProbeResult {
	r := services.Check(ctx, serviceCheckConfig(m.MonitorInput))
	return ProbeResult{State: r.State, LatencyMs: r.LatencyMs, Message: r.Message}
}
