package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

type DependencyResolver interface {
	ResolveWorkflowVersion(versionID string) (workflowID, checksum string, definition Definition, ok bool)
}

func Validate(definition Definition, registry *Registry, dependencies DependencyResolver) error {
	return validate(definition, registry, dependencies, map[string]bool{}, 0)
}

func validate(definition Definition, registry *Registry, dependencies DependencyResolver, ancestry map[string]bool, depth int) error {
	if definition.FormatVersion != FormatVersion || len(definition.Nodes) == 0 || len(definition.Nodes) > 1000 || len(definition.Edges) > 5000 || depth > 16 {
		return ErrInvalidDefinition
	}
	capabilities := map[string]Risk{}
	for _, requirement := range definition.Capabilities {
		if !validToken(requirement.Capability, 160) || !validRisk(requirement.Risk) {
			return ErrInvalidDefinition
		}
		capabilities[requirement.Capability] = requirement.Risk
	}
	nodes := map[string]Node{}
	descriptors := map[string]NodeDescriptor{}
	for _, node := range definition.Nodes {
		if !validToken(node.ID, 160) || !validToken(node.Kind, 120) || node.KindVersion < 1 || nodes[node.ID].ID != "" || !validObject(node.Config) || !validSchema(node.OutputSchema) {
			return fmt.Errorf("%w: invalid node %q", ErrInvalidDefinition, node.ID)
		}
		if node.Retry.MaxAttempts == 0 && node.Retry.CooldownSeconds == 0 {
			node.Retry = DefaultRetryPolicy()
		}
		if node.Retry.MaxAttempts != 3 || node.Retry.CooldownSeconds != 60 {
			return fmt.Errorf("%w: invalid retry policy on %s", ErrInvalidDefinition, node.ID)
		}
		if node.Errors.Mode != "" && node.Errors.Mode != "fail" && node.Errors.Mode != "continue" && node.Errors.Mode != "collect" {
			return fmt.Errorf("%w: invalid error policy on %s", ErrInvalidDefinition, node.ID)
		}
		if registry != nil {
			descriptor, ok := registry.Resolve(node.Kind, node.KindVersion)
			if !ok {
				return fmt.Errorf("%w: %s@%d", ErrProviderMissing, node.Kind, node.KindVersion)
			}
			grantedRisk, granted := capabilities[descriptor.Capability]
			if !granted || riskRank(grantedRisk) < riskRank(descriptor.Risk) {
				return fmt.Errorf("%w: %s requires %s", ErrCapabilityDenied, node.ID, descriptor.Capability)
			}
			descriptors[node.ID] = descriptor
		}
		nodes[node.ID] = node
	}
	dependencyIDs := map[string]bool{}
	for _, dependency := range definition.Dependencies {
		dependencyIDs[dependency.VersionID] = true
	}
	for _, node := range definition.Nodes {
		switch node.Kind {
		case "call_workflow":
			var config struct {
				WorkflowVersionID string `json:"workflowVersionId"`
			}
			if json.Unmarshal(node.Config, &config) != nil || config.WorkflowVersionID == "" || !dependencyIDs[config.WorkflowVersionID] {
				return fmt.Errorf("%w: call_workflow %s must pin a declared dependency", ErrInvalidDefinition, node.ID)
			}
		case "for_each":
			var config TestingForEachConfig
			if json.Unmarshal(node.Config, &config) != nil || (config.ChildGraph == nil) == (config.WorkflowVersionID == "") {
				return fmt.Errorf("%w: for_each %s requires exactly one child graph or pinned subflow", ErrInvalidDefinition, node.ID)
			}
			if config.WorkflowVersionID != "" && !dependencyIDs[config.WorkflowVersionID] {
				return fmt.Errorf("%w: for_each %s subflow is not declared", ErrInvalidDefinition, node.ID)
			}
			if config.ChildGraph != nil {
				if err := validate(*config.ChildGraph, registry, dependencies, cloneSet(ancestry), depth+1); err != nil {
					return err
				}
			}
		}
	}
	for _, node := range definition.Nodes {
		for inputName, binding := range node.Inputs {
			if !validToken(inputName, 120) {
				return fmt.Errorf("%w: invalid input binding on %s", ErrInvalidDefinition, node.ID)
			}
			sources := 0
			if binding.SourceNode != "" {
				sources++
				upstream, exists := nodes[binding.SourceNode]
				if !exists || !validToken(binding.SourcePort, 120) || !schemaHasPort(upstream.OutputSchema, binding.SourcePort) {
					return fmt.Errorf("%w: invalid source binding %s.%s", ErrInvalidDefinition, binding.SourceNode, binding.SourcePort)
				}
			}
			if binding.InputPath != "" {
				sources++
				if !validToken(binding.InputPath, 200) {
					return fmt.Errorf("%w: invalid input path", ErrInvalidDefinition)
				}
			}
			if binding.Literal != nil {
				sources++
			}
			if sources != 1 || !schemaHasPort(descriptors[node.ID].InputSchema, inputName) {
				return fmt.Errorf("%w: ambiguous or unknown input binding %s.%s", ErrInvalidDefinition, node.ID, inputName)
			}
		}
	}
	edgeIDs := map[string]bool{}
	indegree := map[string]int{}
	outgoing := map[string][]string{}
	boundPorts := map[string]bool{}
	for id := range nodes {
		indegree[id] = 0
	}
	for _, edge := range definition.Edges {
		if !validToken(edge.ID, 200) || edgeIDs[edge.ID] || nodes[edge.Source].ID == "" || nodes[edge.Target].ID == "" || edge.Source == edge.Target || !validToken(edge.SourcePort, 120) || !validToken(edge.TargetPort, 120) {
			return fmt.Errorf("%w: invalid edge %q", ErrInvalidDefinition, edge.ID)
		}
		portKey := edge.Target + ":" + edge.TargetPort
		if boundPorts[portKey] {
			return fmt.Errorf("%w: target port %s has multiple bindings", ErrInvalidDefinition, portKey)
		}
		if !schemaHasPort(nodes[edge.Source].OutputSchema, edge.SourcePort) || !schemaHasPort(descriptors[edge.Target].InputSchema, edge.TargetPort) {
			return fmt.Errorf("%w: incompatible edge ports %s", ErrInvalidDefinition, edge.ID)
		}
		boundPorts[portKey], edgeIDs[edge.ID] = true, true
		indegree[edge.Target]++
		outgoing[edge.Source] = append(outgoing[edge.Source], edge.Target)
	}
	queue := []string{}
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, target := range outgoing[id] {
			indegree[target]--
			if indegree[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	if visited != len(nodes) {
		return fmt.Errorf("%w: workflow contains a cycle", ErrInvalidDefinition)
	}
	for _, dependency := range definition.Dependencies {
		if !validToken(dependency.WorkflowID, 200) || !validToken(dependency.VersionID, 200) || len(dependency.Checksum) != 64 || dependencies == nil {
			return fmt.Errorf("%w: invalid dependency", ErrInvalidDefinition)
		}
		workflowID, checksum, child, ok := dependencies.ResolveWorkflowVersion(dependency.VersionID)
		if !ok || workflowID != dependency.WorkflowID || checksum != dependency.Checksum || ancestry[dependency.VersionID] {
			return fmt.Errorf("%w: unresolved or recursive dependency %s", ErrInvalidDefinition, dependency.VersionID)
		}
		next := cloneSet(ancestry)
		next[dependency.VersionID] = true
		if err := validate(child, registry, dependencies, next, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func schemaHasPort(schema JSONSchema, port string) bool {
	if port == "output" || port == "input" {
		return true
	}
	properties, ok := schemaMap(schema["properties"])
	if !ok || len(properties) == 0 {
		return true
	}
	_, ok = properties[port]
	return ok
}

func validObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func validSchema(schema JSONSchema) bool {
	if schema == nil {
		return false
	}
	raw, err := json.Marshal(schema)
	return err == nil && len(raw) <= 256*1024
}

func validRisk(risk Risk) bool {
	return risk == RiskRead || risk == RiskWrite || risk == RiskDestructive
}
func riskRank(risk Risk) int {
	if risk == RiskDestructive {
		return 3
	}
	if risk == RiskWrite {
		return 2
	}
	return 1
}

func validToken(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if !(character == '-' || character == '_' || character == '.' || character == ':' || character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z') {
			return false
		}
	}
	return true
}

func cloneSet(source map[string]bool) map[string]bool {
	out := map[string]bool{}
	for key, value := range source {
		out[key] = value
	}
	return out
}
