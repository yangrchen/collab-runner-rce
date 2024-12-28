package types

type AgentRunRequest struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

type AgentRunResponse struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}
