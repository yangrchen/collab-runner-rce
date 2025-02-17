package types

type AgentRunRequest struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

type AgentRunResponse struct {
	ClientRes         ClientResponse `json:"client_response"`
	StateFileEndpoint string         `json:"state_file_endpoint"`
	StateFile         string         `json:state_file`
}

type ClientResponse struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}
