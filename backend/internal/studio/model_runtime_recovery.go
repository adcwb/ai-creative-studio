package studio

import "net/http"

func setRuntimeReadOnly(client *http.Client, allow func(*http.Request) bool) {
	transport := client.Transport.(runtimeTransport)
	transport.allowReadOnly = allow
	client.Transport = transport
}

// Recovery never receives broad GET access to an account. Both the trusted
// adapter and the transport restrict requests to the persisted upstream ID.
func videoReadOnlyRequest(provider string) func(*http.Request) bool {
	return func(req *http.Request) bool {
		id := executionRecoveryUpstream(req.Context())
		if id == "" || req.Method != http.MethodGet || req.ContentLength != 0 || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.RawPath != "" || req.URL.Fragment != "" {
			return false
		}
		switch provider {
		case "ark-video":
			return arkVideoIDPattern.MatchString(id) && req.URL.Host == "ark.cn-beijing.volces.com" && req.URL.Path == "/api/v3/contents/generations/tasks/"+id && req.URL.RawQuery == ""
		case "minimax-video":
			return miniMaxVideoIDPattern.MatchString(id) && req.URL.Host == "api.minimax.io" && req.URL.Path == "/v2/query/video_generation/"+id && req.URL.RawQuery == ""
		case "gemini-video":
			return googleFileID.MatchString(id) && req.URL.Host == "generativelanguage.googleapis.com" && ((req.URL.Path == "/v1beta/files/"+id && req.URL.RawQuery == "") || (req.URL.Path == "/v1beta/files/"+id+":download" && req.URL.RawQuery == "alt=media"))
		}
		return false
	}
}
