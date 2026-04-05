package ipc

type Request struct {
	Token  string `json:"token"`
	Method string `json:"method"`
	Body   any    `json:"body,omitempty"`
}

type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Body  any    `json:"body,omitempty"`
}

type Status struct {
	Running            bool   `json:"running"`
	CurrentLogFile     string `json:"current_log_file"`
	CurrentOffset      int64  `json:"current_offset"`
	LastEventAtRFC3339 string `json:"last_event_at_rfc3339"`
}
