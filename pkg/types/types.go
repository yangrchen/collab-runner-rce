package types

type AgentRunRequest struct {
	ID        string   `json:"id"`
	Code      string   `json:"code"`
	SourceIds []string `json:"sourceIds"`
}

type AgentRunResponse struct {
	ClientRes         ClientResponse `json:"clientResponse"`
	Error             AgentError     `json:"error"`
	StateFileEndpoint string         `json:"stateFileEndpoint"`
	StateFile         string         `json:"stateFile"`
}

type AgentError struct {
	Message string `json:"message"`
	Context string `json:"context"`
}

func (a *AgentError) Error() string {
	return a.Message
}

func (a *AgentError) GetContext() string {
	return a.Context
}

type ClientResponse struct {
	Result string `json:"result"`
	Error  string `json:"error"`
}
