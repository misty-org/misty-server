package capabilities

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed builtins.json
var builtinJSON []byte

var builtinContracts = loadBuiltinContracts()

func loadBuiltinContracts() map[string]map[int]Definition {
	var definitions []Definition
	if err := json.Unmarshal(builtinJSON, &definitions); err != nil {
		panic(err)
	}
	contracts := map[string]map[int]Definition{}
	for _, definition := range definitions {
		if contracts[definition.Name] == nil {
			contracts[definition.Name] = map[int]Definition{}
		}
		if _, exists := contracts[definition.Name][definition.Version]; exists {
			panic("duplicate built-in capability version")
		}
		contracts[definition.Name][definition.Version] = definition
	}
	return contracts
}
func validateBuiltinContract(definition Definition) error {
	versions, reserved := builtinContracts[definition.Name]
	if !reserved {
		return nil
	}
	builtin, exists := versions[definition.Version]
	if !exists {
		return fmt.Errorf("%w: unsupported built-in capability version", ErrInvalid)
	}
	proposed, _ := json.Marshal(definition)
	expected, _ := json.Marshal(builtin)
	if !EqualJSON(proposed, expected) {
		return fmt.Errorf("%w: built-in capability meaning cannot be changed by a provider", ErrInvalid)
	}
	return nil
}
