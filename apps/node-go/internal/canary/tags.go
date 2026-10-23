package canary

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

const resourceTag = "tag"

type TagInput struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Tag struct {
	TagInput
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	UpdatedAt int64  `json:"updatedAt"`
	Deleted   bool   `json:"deleted,omitempty"`
}

type TagView struct {
	Tag            Tag  `json:"tag"`
	ConfigConflict bool `json:"configConflict"`
}

type MonitorTag struct {
	TagID  string `json:"tagId"`
	Value  string `json:"value,omitempty"`
	Public bool   `json:"public,omitempty"`
}

type PublicStatusTag struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Value string `json:"value,omitempty"`
}

func (a *MonitorTag) UnmarshalJSON(b []byte) error {
	type plain MonitorTag
	var out plain
	if err := decodeMonitorOptions(b, &out, "tagId", "value", "public"); err != nil {
		return err
	}
	*a = MonitorTag(out)
	return nil
}

func parseTag(b []byte) (TagInput, error) {
	var raw struct {
		TagInput
		ID        string   `json:"id"`
		Revision  string   `json:"revision"`
		Revisions []string `json:"revisions"`
		UpdatedAt int64    `json:"updatedAt"`
		Deleted   bool     `json:"deleted"`
	}
	bad := func() (TagInput, error) { return TagInput{}, fail(400, "Invalid tag input") }
	if decodeMonitorOptions(b, &raw, "name", "color", "id", "revision", "revisions", "updatedAt", "deleted") != nil {
		return bad()
	}
	o, _ := jsonObject(b)
	if required(o, "name", "color") != nil || validText(raw.Name, "tag name", 128) != nil || len(raw.Color) != 7 || raw.Color[0] != '#' {
		return bad()
	}
	for _, c := range raw.Color[1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return bad()
		}
	}
	if o["id"] != nil && identifier(raw.ID, "id") != nil || o["revision"] != nil && identifier(raw.Revision, "revision") != nil || o["updatedAt"] != nil && !validTime(raw.UpdatedAt) || o["revisions"] != nil && (!validIDs(raw.Revisions, maxAPIBody) || len(unique(raw.Revisions)) != len(raw.Revisions)) {
		return bad()
	}
	raw.Color = strings.ToLower(raw.Color)
	return raw.TagInput, nil
}

func tagOf(r Resource) Tag {
	in, _ := parseTag(r.Data)
	return Tag{TagInput: in, ID: r.ID, Revision: r.Revision, UpdatedAt: r.UpdatedAt, Deleted: r.Deleted}
}

func (n *Node) tagViews() []TagView {
	out := []TagView{}
	for _, heads := range n.resources {
		for _, h := range heads {
			if h.Resource.Kind == resourceTag && !h.Resource.Deleted {
				out = append(out, TagView{Tag: tagOf(h.Resource), ConfigConflict: len(heads) != 1 || !n.resourceVersionReady(h)})
				break
			}
		}
	}
	slices.SortFunc(out, func(a, b TagView) int { return stringsCompare(a.Tag.Name, b.Tag.Name) })
	return out
}

func (n *Node) publicMonitorTags(m Monitor) []PublicStatusTag {
	out := []PublicStatusTag{}
	for _, a := range m.Tags {
		if !a.Public {
			continue
		}
		if r := n.currentResource(resourceTag, a.TagID); r != nil {
			t := tagOf(*r)
			out = append(out, PublicStatusTag{Name: t.Name, Color: t.Color, Value: a.Value})
		}
	}
	return out
}

func (n *Node) tagAPI(r *http.Request, a account, b []byte) (int, any, error) {
	method, path := r.Method, r.URL.Path
	if method != "GET" && a.Role != "admin" && a.Role != "editor" {
		return 0, nil, fail(403, "Insufficient permissions")
	}
	if path == "/api/tags" {
		if method == "GET" {
			return 200, n.tagViews(), nil
		}
		if method != "POST" {
			return 0, nil, fail(404, "Not found")
		}
		if len(n.resources) >= maxResources {
			return 0, nil, fail(409, "Resource capacity exceeded")
		}
		in, err := parseTag(b)
		if err != nil {
			return 0, nil, err
		}
		saved, err := n.writeResource(resourceTag, "", in, false)
		return 201, tagOf(saved), err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/tags/"), "/")
	if len(parts) < 1 || len(parts) > 2 || identifier(parts[0], "tag id") != nil {
		return 0, nil, fail(404, "Not found")
	}
	heads := n.resources[resourceKey(resourceTag, parts[0])]
	if len(heads) == 0 {
		return 0, nil, fail(404, "Tag not found")
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if method == "GET" && action == "conflicts" {
		out := []Tag{}
		for _, h := range heads {
			out = append(out, tagOf(h.Resource))
		}
		return 200, out, nil
	}
	if method == "GET" && action == "" {
		if len(heads) != 1 || !n.resourceVersionReady(heads[0]) {
			return 0, nil, fail(409, "Tag is conflicting or synchronizing")
		}
		if heads[0].Resource.Deleted {
			return 0, nil, fail(404, "Tag not found")
		}
		return 200, tagOf(heads[0].Resource), nil
	}
	if !(method == "POST" && action == "resolve" || action == "" && (method == "PUT" || method == "DELETE")) {
		return 0, nil, fail(404, "Not found")
	}
	var rev struct {
		Revision  string   `json:"revision"`
		Revisions []string `json:"revisions"`
	}
	var in TagInput
	var err error
	if method == "DELETE" {
		if decodeMonitorOptions(b, &rev, "revision") != nil {
			return 0, nil, fail(400, "Invalid tag revision")
		}
	} else if in, err = parseTag(b); err != nil {
		return 0, nil, err
	}
	if json.Unmarshal(b, &rev) != nil {
		return 0, nil, fail(400, "Invalid tag revision")
	}
	if action == "resolve" {
		if len(heads) < 2 || len(rev.Revisions) != len(heads) || len(unique(rev.Revisions)) != len(heads) {
			return 0, nil, fail(409, "Resolution must name every current head exactly once")
		}
		for _, h := range heads {
			if !slices.Contains(rev.Revisions, h.Resource.Revision) || !n.resourceVersionReady(h) {
				return 0, nil, fail(409, "Resolution requires complete current heads")
			}
		}
	} else if len(heads) != 1 || heads[0].Resource.Revision != rev.Revision || !n.resourceVersionReady(heads[0]) {
		return 0, nil, fail(409, "Tag revision is stale or conflicting")
	} else if heads[0].Resource.Deleted {
		return 0, nil, fail(404, "Tag not found")
	}
	if method == "DELETE" {
		_, err := n.writeResource(resourceTag, parts[0], tagOf(heads[0].Resource).TagInput, true)
		return 204, nil, err
	}
	saved, err := n.writeResource(resourceTag, parts[0], in, false)
	return 200, tagOf(saved), err
}
