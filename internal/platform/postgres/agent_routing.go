package db

type RoutingOption struct {
	SpaceID        string `json:"space_id"`
	SpaceName      string `json:"space_name"`
	AgentID        string `json:"agent_id"`
	AgentName      string `json:"agent_name"`
	CapabilityID   string `json:"capability_id"`
	CapabilityName string `json:"capability_name"`
}

type RoutingDecision struct {
	NeedsClarification bool            `json:"needs_clarification"`
	Question           string          `json:"question,omitempty"`
	Options            []RoutingOption `json:"options,omitempty"`
	Selected           *RoutingOption  `json:"selected,omitempty"`
	Reason             string          `json:"reason,omitempty"`
}
