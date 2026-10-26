package canary

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	providerDelivery "uptime-canary/apps/node-go/internal/delivery"
)

type ChannelInput struct {
	Name      string                  `json:"name"`
	Enabled   bool                    `json:"enabled"`
	Config    providerDelivery.Config `json:"config"`
	IsDefault bool                    `json:"isDefault,omitempty"`
}

type Channel struct {
	ChannelInput
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	UpdatedAt int64  `json:"updatedAt"`
	Deleted   bool   `json:"deleted,omitempty"`
}

type ChannelView struct {
	Channel        Channel `json:"channel"`
	ConfigConflict bool    `json:"configConflict"`
}

func parseChannel(b []byte) (ChannelInput, error) {
	var in ChannelInput
	raw, err := strictResourceObject(b, "name", "enabled", "config", "id", "revision", "revisions", "updatedAt", "deleted", "isDefault")
	if err != nil || required(raw, "name", "enabled", "config") != nil {
		return in, fail(400, "Channel requires name, enabled and config")
	}
	if json.Unmarshal(raw["name"], &in.Name) != nil || json.Unmarshal(raw["enabled"], &in.Enabled) != nil || validText(in.Name, "channel name", 128) != nil {
		return in, fail(400, "Invalid channel name or enabled flag")
	}
	if raw["isDefault"] != nil && json.Unmarshal(raw["isDefault"], &in.IsDefault) != nil {
		return in, fail(400, "Invalid default channel flag")
	}
	d := json.NewDecoder(bytes.NewReader(raw["config"]))
	d.DisallowUnknownFields()
	if d.Decode(&in.Config) != nil {
		return in, fail(400, "Invalid notification provider configuration")
	}
	if in.Config.Kind == "discord" {
		if discordWebhookURL(in.Config.Webhook.URL) == nil || len(in.Config.Recipients) != 0 || in.Config.Webhook.BearerToken != "" {
			return in, fail(400, "Discord requires a valid Discord webhook URL")
		}
		if !providerDelivery.ValidateTemplates(in.Config.Templates) {
			return in, fail(400, "Invalid Discord message templates")
		}
	} else if providerDelivery.Validate(in.Config) != nil {
		return in, fail(400, "Invalid notification provider configuration")
	}
	return in, nil
}

func channelOf(r Resource, role string) Channel {
	var in ChannelInput
	_ = json.Unmarshal(r.Data, &in)
	if role == "viewer" {
		in.Config = providerDelivery.Config{Kind: in.Config.Kind}
	}
	return Channel{ChannelInput: in, ID: r.ID, Revision: r.Revision, UpdatedAt: r.UpdatedAt, Deleted: r.Deleted}
}

func (n *Node) channelViews(role string) []ChannelView {
	out := []ChannelView{}
	for _, heads := range n.resources {
		for _, h := range heads {
			if h.Resource.Kind == resourceChannel && !h.Resource.Deleted {
				out = append(out, ChannelView{Channel: channelOf(h.Resource, role), ConfigConflict: len(heads) > 1 || !n.resourceVersionReady(h)})
				break
			}
		}
	}
	slices.SortFunc(out, func(a, b ChannelView) int { return stringsCompare(a.Channel.Name, b.Channel.Name) })
	return out
}

func (n *Node) channelAPI(r *http.Request, a account, b []byte) (int, any, error) {
	path, method := r.URL.Path, r.Method
	if path == "/api/notification-channels/preview-test" && method == "POST" {
		if a.Role == "viewer" {
			return 0, nil, fail(403, "Insufficient permissions")
		}
		in, err := parseChannel(b)
		if err != nil {
			return 0, nil, err
		}
		return n.queueChannelTest(Channel{ChannelInput: in, ID: "preview-" + newID(), Revision: newID()}, true)
	}
	if path == "/api/notification-channels" {
		if method == "GET" {
			return 200, n.channelViews(a.Role), nil
		}
		if method != "POST" {
			return 0, nil, fail(404, "Not found")
		}
		if len(n.resources) >= maxResources {
			return 0, nil, fail(409, "Resource capacity exceeded")
		}
		in, err := parseChannel(b)
		if err != nil {
			return 0, nil, err
		}
		saved, err := n.writeResource(resourceChannel, "", in, false)
		return 201, channelOf(saved, a.Role), err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/notification-channels/"), "/")
	if len(parts) < 1 || len(parts) > 2 || identifier(parts[0], "id") != nil {
		return 0, nil, fail(404, "Not found")
	}
	id := parts[0]
	heads := n.resources[resourceKey(resourceChannel, id)]
	if len(heads) == 0 {
		return 0, nil, fail(404, "Notification channel not found")
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if method == "GET" && action == "conflicts" {
		out := []Channel{}
		for _, h := range heads {
			out = append(out, channelOf(h.Resource, a.Role))
		}
		return 200, out, nil
	}
	if method == "GET" && action == "" && len(heads) == 1 && !heads[0].Resource.Deleted {
		return 200, channelOf(heads[0].Resource, a.Role), nil
	}
	if a.Role == "viewer" {
		return 0, nil, fail(403, "Insufficient permissions")
	}
	if method == "POST" && action == "test" {
		return n.startChannelTest(id, b)
	}
	if method == "POST" && action == "apply" {
		return n.applyChannel(id, b)
	}
	if !(action == "resolve" && method == "POST" || action == "" && (method == "PUT" || method == "DELETE")) {
		return 0, nil, fail(404, "Not found")
	}
	var revisions struct {
		Revision  string   `json:"revision"`
		Revisions []string `json:"revisions"`
	}
	if json.Unmarshal(b, &revisions) != nil {
		return 0, nil, fail(400, "Invalid channel revision")
	}
	if action == "resolve" {
		if len(heads) < 2 || len(revisions.Revisions) != len(heads) || len(unique(revisions.Revisions)) != len(heads) {
			return 0, nil, fail(409, "Resolution must name every current head exactly once")
		}
		for _, h := range heads {
			if !slices.Contains(revisions.Revisions, h.Resource.Revision) {
				return 0, nil, fail(409, "Resolution must name every current head exactly once")
			}
		}
	} else if len(heads) != 1 || heads[0].Resource.Revision != revisions.Revision {
		return 0, nil, fail(409, "Channel revision is stale or conflicting")
	} else if heads[0].Resource.Deleted {
		return 0, nil, fail(404, "Notification channel was deleted")
	}
	if method == "DELETE" {
		saved, err := n.writeResource(resourceChannel, id, channelOf(heads[0].Resource, "admin").ChannelInput, true)
		_ = saved
		return 204, nil, err
	}
	in, err := parseChannel(b)
	if err != nil {
		return 0, nil, err
	}
	saved, err := n.writeResource(resourceChannel, id, in, false)
	return 200, channelOf(saved, a.Role), err
}
