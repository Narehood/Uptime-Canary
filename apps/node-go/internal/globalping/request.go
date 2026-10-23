package globalping

// These private wire types deliberately separate provider credentials from
// target headers. The API token can appear only in the API Authorization header.
type createRequest struct {
	Type               string             `json:"type"`
	Target             string             `json:"target"`
	Limit              int                `json:"limit"`
	Locations          []location         `json:"locations"`
	InProgressUpdates  bool               `json:"inProgressUpdates"`
	MeasurementOptions measurementOptions `json:"measurementOptions"`
	Timeout            int                `json:"timeout,omitempty"`
}

type location struct {
	Magic string `json:"magic"`
}
type queryOptions struct {
	Type string `json:"type"`
}
type requestOptions struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}
type measurementOptions struct {
	Packets   int             `json:"packets,omitempty"`
	Protocol  string          `json:"protocol"`
	Port      int             `json:"port,omitempty"`
	IPVersion int             `json:"ipVersion,omitempty"`
	Resolver  string          `json:"resolver,omitempty"`
	Query     *queryOptions   `json:"query,omitempty"`
	Request   *requestOptions `json:"request,omitempty"`
}
